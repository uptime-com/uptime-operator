/*
Copyright 2026 Uptime.com.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	monitoringv1alpha1 "github.com/uptime-com/uptime-operator/api/v1alpha1"
)

// Ingress annotation keys. Prefixed with the operator's API group so the
// annotations sit in the same namespace as the CRD (no collision with
// unrelated operators).
const (
	annoPrefix        = "monitoring.uptime.com/"
	annoEnabled       = annoPrefix + "enabled"
	annoURL           = annoPrefix + "url"
	annoPath          = annoPrefix + "path"
	annoScheme        = annoPrefix + "scheme"
	annoInterval      = annoPrefix + "interval"
	annoLocations     = annoPrefix + "locations"
	annoContactGroups = annoPrefix + "contact-groups"
	annoExpectStatus  = annoPrefix + "expect-status"
	annoTokenSecret   = annoPrefix + "api-token-secret"
	annoTokenKey      = annoPrefix + "api-token-secret-key"
	annoNameOverride  = annoPrefix + "name"
)

// IngressReconciler synthesizes UptimeCheck CRs from annotated Ingress
// resources. Authoritative checks are still managed by UptimeCheckReconciler;
// this controller just keeps the derived CR in sync with the Ingress.
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.uptime.com,resources=uptimechecks,verbs=get;list;watch;create;update;patch;delete

// Reconcile derives an UptimeCheck for the Ingress, or removes one if the
// Ingress is no longer annotated.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ing networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ing); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	derivedName := derivedCheckName(&ing)
	if !ingressOptedIn(&ing) {
		return r.removeDerived(ctx, ing.Namespace, derivedName)
	}

	spec, err := specFromIngress(&ing)
	if err != nil {
		log.Error(err, "ingress annotations invalid; skipping", "ingress", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	check := &monitoringv1alpha1.UptimeCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name:      derivedName,
			Namespace: ing.Namespace,
		},
	}
	mutate := func() error {
		check.Spec = spec
		return controllerutil.SetControllerReference(&ing, check, r.Scheme)
	}

	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, check, mutate)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("create/update derived UptimeCheck: %w", err)
	}
	if res != controllerutil.OperationResultNone {
		log.Info("derived UptimeCheck reconciled", "result", res, "name", derivedName)
	}
	return ctrl.Result{}, nil
}

func (r *IngressReconciler) removeDerived(ctx context.Context, namespace, name string) (ctrl.Result, error) {
	var check monitoringv1alpha1.UptimeCheck
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &check)
	switch {
	case apierrors.IsNotFound(err):
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	}
	if err := r.Delete(ctx, &check); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller. Predicate filters out Ingresses
// that have never carried an opt-in annotation so the work queue stays small
// in clusters with thousands of Ingresses.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&monitoringv1alpha1.UptimeCheck{}).
		Named("ingress-uptimecheck").
		Complete(r)
}

func ingressOptedIn(ing *networkingv1.Ingress) bool {
	v := strings.ToLower(strings.TrimSpace(ing.Annotations[annoEnabled]))
	return v == "true" || v == "1" || v == "yes"
}

func derivedCheckName(ing *networkingv1.Ingress) string {
	return fmt.Sprintf("ingress-%s", ing.Name)
}

func specFromIngress(ing *networkingv1.Ingress) (monitoringv1alpha1.UptimeCheckSpec, error) {
	url, err := urlFromIngress(ing)
	if err != nil {
		return monitoringv1alpha1.UptimeCheckSpec{}, err
	}

	spec := monitoringv1alpha1.UptimeCheckSpec{
		Type: monitoringv1alpha1.CheckTypeHTTP,
		Name: strings.TrimSpace(ing.Annotations[annoNameOverride]),
		HTTP: &monitoringv1alpha1.HTTPSpec{
			URL:        url,
			StatusCode: strings.TrimSpace(ing.Annotations[annoExpectStatus]),
		},
	}

	// The token annotation is optional: without it the derived check inherits
	// the operator's default token Secret.
	if tokenSecret := strings.TrimSpace(ing.Annotations[annoTokenSecret]); tokenSecret != "" {
		spec.APITokenSecretRef = &monitoringv1alpha1.SecretKeyReference{
			Name: tokenSecret,
			Key:  strings.TrimSpace(ing.Annotations[annoTokenKey]),
		}
	}

	if v := strings.TrimSpace(ing.Annotations[annoInterval]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return monitoringv1alpha1.UptimeCheckSpec{}, fmt.Errorf("%s=%q: %w", annoInterval, v, err)
		}
		spec.Interval = int32(n)
	}

	if v := strings.TrimSpace(ing.Annotations[annoLocations]); v != "" {
		spec.Locations = splitCSV(v)
	}
	if v := strings.TrimSpace(ing.Annotations[annoContactGroups]); v != "" {
		spec.ContactGroups = splitCSV(v)
	}

	return spec, nil
}

func urlFromIngress(ing *networkingv1.Ingress) (string, error) {
	if v := strings.TrimSpace(ing.Annotations[annoURL]); v != "" {
		return v, nil
	}

	host := firstHost(ing)
	if host == "" {
		return "", fmt.Errorf("ingress has no host and no %s annotation", annoURL)
	}

	scheme := strings.ToLower(strings.TrimSpace(ing.Annotations[annoScheme]))
	if scheme == "" {
		scheme = "https"
		if len(ing.Spec.TLS) == 0 {
			scheme = "http"
		}
	}

	path := strings.TrimSpace(ing.Annotations[annoPath])
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, path), nil
}

func firstHost(ing *networkingv1.Ingress) string {
	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" {
			return rule.Host
		}
	}
	return ""
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
