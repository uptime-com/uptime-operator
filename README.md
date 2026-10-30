# uptime-operator

A Kubernetes operator that manages [Uptime.com](https://uptime.com) monitoring checks declaratively
from CRDs and `Ingress` annotations.

## What it does

- **`UptimeCheck` CRD** - declare an Uptime.com check (HTTP today; DNS/ICMP/Heartbeat planned) and
  the operator creates, updates, and deletes the corresponding remote check via the Uptime.com API.
- **Ingress watcher** - `Ingress` resources annotated with `monitoring.uptime.com/check: "true"`
  get a matching `UptimeCheck` created automatically, following the `Ingress` lifecycle.
- **Finalizer-safe deletion** - when a CR is deleted, the operator removes the remote check before
  letting the Kubernetes object go, so checks don't leak.

## Installation

Each `vX.Y.Z` git tag publishes:

- Image: `ghcr.io/uptime-com/uptime-operator:X.Y.Z` (multi-arch: `amd64`, `arm64`)
- Chart: `oci://ghcr.io/uptime-com/charts/uptime-operator`, version `X.Y.Z`
- Manifest bundle: `install.yaml` attached to the GitHub Release

Note that artifact versions are bare SemVer (`X.Y.Z`), while git tags carry the `v` prefix.
Browse available versions on the
[Releases page](https://github.com/uptime-com/uptime-operator/releases).

### Helm (recommended)

Requires Helm 3.8+ (OCI registries). Public charts need no `helm registry login`.

```sh
helm install uptime-operator oci://ghcr.io/uptime-com/charts/uptime-operator \
  --version X.Y.Z \
  --namespace uptime-operator-system --create-namespace
```

The chart's default `manager.image.repository` points at the public GHCR image; the tag defaults to
the chart `appVersion`. Override either with `--set manager.image.repository=...` or
`--set manager.image.tag=...` if you mirror the image elsewhere.

`make helm-deploy`, `helm-status`, `helm-uninstall`, `helm-history`, `helm-rollback` wrap the
in-tree chart at `dist/chart/` for development; see `Makefile`.

### kubectl bundle

```sh
kubectl apply -f \
  https://github.com/uptime-com/uptime-operator/releases/download/vX.Y.Z/install.yaml
```

Same kustomize render as `make build-installer`, pinned to the released image.

## Quickstart

The sample bundle ships a placeholder `Secret` named `uptime-token` plus an example `UptimeCheck`.
Edit `config/samples/monitoring_v1alpha1_uptimecheck.yaml` to set your real Uptime.com API token,
then apply:

```sh
kubectl apply -k config/samples/
kubectl get uptimechecks
```

Example `UptimeCheck`:

```yaml
apiVersion: monitoring.uptime.com/v1alpha1
kind: UptimeCheck
metadata:
  name: example-health
spec:
  type: HTTP
  interval: 5
  contactGroups: [Default]
  locations:
    - US-NY-New York
    - US-CA-Los Angeles
    - US-TX-Dallas
    - United Kingdom-London
    - Austria-Vienna
  apiTokenSecretRef:
    name: uptime-token
    key: token
  http:
    url: https://example.com/healthz
    statusCode: "200"
    encryption: ssl_verify
```

`spec.locations` is required by the Uptime.com API - the call fails with a
`VALIDATION_ERROR` if it's empty. Pick at least one probe location name from
`GET /api/v1/probe-servers/`. The five locations above are a reasonable default
spread (US east/west/central, UK, Austria); adjust to match your account's
plan.

Set `spec.apiURL` to point at a non-production Uptime.com instance, e.g. the sandbox.

## API token

Each check needs an Uptime.com API token. There are two ways to provide one, and
`spec.apiTokenSecretRef` is optional precisely so you can pick either.

**Per resource** - `spec.apiTokenSecretRef` names a `Secret` in the *same
namespace* as the `UptimeCheck` (key `token` by default). Use this when
different teams or namespaces own different Uptime.com accounts.

**Operator-wide default** - start the manager with
`--default-api-token-secret-name`, and every `UptimeCheck` that omits
`spec.apiTokenSecretRef` uses that `Secret`. It is always read from the
operator's own namespace, whatever namespace the check lives in - there is no
per-namespace override, so a single token can serve the whole cluster.

> **Security:** with a default token configured, anyone who can create an
> `Ingress` annotated `monitoring.uptime.com/enabled` (or an `UptimeCheck`) in
> *any* namespace can create and delete checks on the operator's Uptime.com
> account - without a per-resource fallback they would need a token `Secret` in
> their own namespace. Enable the default only when everyone who can create
> `Ingress` or `UptimeCheck` resources in the cluster is trusted with that
> account.

```sh
kubectl -n uptime-operator-system create secret generic uptime-token \
  --from-literal=token=<your-api-token>

helm upgrade --install uptime-operator oci://ghcr.io/uptime-com/charts/uptime-operator \
  --namespace uptime-operator-system --create-namespace \
  --set defaultApiToken.secretName=uptime-token
```

Flags (all optional):

| Flag | Default | Meaning |
| --- | --- | --- |
| `--default-api-token-secret-name` | *empty* | `Secret` name; empty disables the fallback |
| `--default-api-token-secret-key` | `token` | key inside that `Secret` |

The operator's namespace comes from the `POD_NAMESPACE` downward-API env var
(set by the chart and by `config/manager`); the manager exits at startup if it
is empty while a default token is configured. Running outside a cluster
(`make run`) with a default token therefore needs `POD_NAMESPACE=<ns>` in the
environment.

Precedence is per resource: `spec.apiTokenSecretRef` wins when set, the default
applies otherwise, and a check with neither goes `Ready=False` with reason
`TokenNotConfigured`. The same rules cover `Ingress`-derived checks: the
`monitoring.uptime.com/api-token-secret` annotation is optional too, and its
absence means "use the default".

Caveats:

- **Deletion needs the token too.** Removing a check calls the Uptime.com API
  with the same token that created it. If a check was created through the
  default and the operator is later restarted without
  `--default-api-token-secret-name`, or the default `Secret` is removed,
  deleting the `UptimeCheck` leaves it `Terminating` behind its finalizer, and
  deleting its namespace hangs. Since the `Secret` lives in the operator's
  namespace, the namespace owner can neither see nor fix this: restore the
  default (or set `spec.apiTokenSecretRef`) before deleting such checks.
- **Switching tokens does not move checks.** Removing
  `spec.apiTokenSecretRef` or the `monitoring.uptime.com/api-token-secret`
  annotation silently switches the check to the default token (and vice
  versa). If the two tokens belong to different accounts, the operator gets a
  404 for `status.remoteCheckID`, recreates the check in the new account and
  leaves the old one running in the previous account - delete it there by hand.

## Development

Prereqs: Go (matching `go.mod`), Docker, `kubectl`, `kind`, `helm`, `kubebuilder` v4.14+.

```sh
make manifests generate     # after editing *_types.go or +kubebuilder markers
make lint-fix test          # style + unit tests
make test-e2e               # kind-based e2e (build-tag: e2e)
```

After any change to `config/` (CRDs, RBAC, manager spec), regenerate the chart so it stays in sync:

```sh
kubebuilder edit --plugins=helm/v2-alpha --force
```

The `--force` flag preserves your existing `dist/chart/values.yaml`. See `AGENTS.md` for the full
contributor guide.
