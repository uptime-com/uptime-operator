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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CheckType selects which transport-specific subspec applies. Only HTTP is
// supported in v1alpha1; the discriminator is in place so DNS, ICMP, Heartbeat,
// etc. can be added without a breaking apiVersion bump.
// +kubebuilder:validation:Enum=HTTP
type CheckType string

const (
	CheckTypeHTTP CheckType = "HTTP"
)

// Condition types reported on status.conditions.
const (
	ConditionTypeReady  = "Ready"
	ConditionTypeSynced = "Synced"
)

// Finalizer ensures the remote check is deleted before the K8s object is.
const Finalizer = "monitoring.uptime.com/finalizer"

// UptimeCheckSpec is the declarative description of one Uptime.com check.
type UptimeCheckSpec struct {
	// type selects which transport block (currently only http) is honored.
	// +kubebuilder:validation:Required
	Type CheckType `json:"type"`

	// name is the human-friendly check name shown in the Uptime.com UI.
	// When empty, the operator derives it from "<namespace>/<resource-name>".
	// +optional
	// +kubebuilder:validation:MaxLength=200
	Name string `json:"name,omitempty"`

	// interval is the check frequency in minutes.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=3
	// +kubebuilder:validation:Maximum=1440
	// +optional
	Interval int32 `json:"interval,omitempty"`

	// locations restricts probing to specific Uptime.com regions
	// (e.g. "US East", "EU West"). Empty = Uptime.com defaults.
	// +optional
	Locations []string `json:"locations,omitempty"`

	// contactGroups are Uptime.com contact group names to notify on alert.
	// +optional
	ContactGroups []string `json:"contactGroups,omitempty"`

	// tags are extra Uptime.com tags. The operator additionally injects its
	// own ownership tags - users do not need to manage those.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// apiTokenSecretRef points to a Secret in the same namespace holding the
	// Uptime.com API token under key "token" (override with .key).
	// When omitted, the operator falls back to its own default token Secret
	// (--default-api-token-secret-name); reconciliation fails with
	// TokenNotConfigured if no default is configured either.
	// +optional
	APITokenSecretRef *SecretKeyReference `json:"apiTokenSecretRef,omitempty"`

	// apiURL overrides the Uptime.com API base URL. Defaults to
	// https://uptime.com/api/v1/. Useful for pointing the operator at the
	// sandbox or a self-hosted instance. Must end with /api/v1/.
	// +optional
	// +kubebuilder:validation:Pattern="^https?://.+/api/v1/?$"
	// +kubebuilder:validation:MaxLength=2048
	APIURL string `json:"apiURL,omitempty"`

	// paused stops the check from running without deleting it remotely.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// http is the HTTP-specific configuration. Required when type=HTTP.
	// +optional
	HTTP *HTTPSpec `json:"http,omitempty"`
}

// SecretKeyReference points at one key inside a same-namespace Secret.
type SecretKeyReference struct {
	// name of the Secret resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key inside the Secret's data map.
	// +kubebuilder:default=token
	// +optional
	Key string `json:"key,omitempty"`
}

// HTTPSpec configures an HTTP/HTTPS check.
type HTTPSpec struct {
	// url is the full URL to probe (scheme required).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^https?://"
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url"`

	// statusCode is the expected HTTP status (or comma-separated list).
	// +kubebuilder:default="200"
	// +optional
	StatusCode string `json:"statusCode,omitempty"`

	// expectString fails the check if missing from response body.
	// +optional
	ExpectString string `json:"expectString,omitempty"`

	// sendString is sent as the request body.
	// +optional
	SendString string `json:"sendString,omitempty"`

	// headers in "Key: Value" form, one per line.
	// +optional
	Headers string `json:"headers,omitempty"`

	// numRetries before flipping the check to down.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=5
	// +optional
	NumRetries int32 `json:"numRetries,omitempty"`

	// encryption: ssl, ssl_verify, or empty for none. Applies to https URLs.
	// +kubebuilder:validation:Enum="";ssl;ssl_verify
	// +optional
	Encryption string `json:"encryption,omitempty"`

	// threshold is the response-time threshold in seconds. 0 = no threshold.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Threshold int32 `json:"threshold,omitempty"`
}

// UptimeCheckStatus is the observed state of an UptimeCheck.
type UptimeCheckStatus struct {
	// observedGeneration is the .metadata.generation last successfully
	// reconciled with the Uptime.com API.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// remoteCheckID is the Uptime.com primary key for the managed check.
	// Used to address subsequent update/delete calls without searching.
	// +optional
	RemoteCheckID int64 `json:"remoteCheckID,omitempty"`

	// remoteCheckURL is the Uptime.com API URL of the managed check.
	// +optional
	RemoteCheckURL string `json:"remoteCheckURL,omitempty"`

	// conditions report the current state of the resource.
	// Types: Ready (overall health), Synced (last reconcile outcome).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.http.url"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="RemoteID",type=integer,JSONPath=".status.remoteCheckID"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// UptimeCheck is the Schema for the uptimechecks API
type UptimeCheck struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of UptimeCheck
	// +required
	Spec UptimeCheckSpec `json:"spec"`

	// status defines the observed state of UptimeCheck
	// +optional
	Status UptimeCheckStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// UptimeCheckList contains a list of UptimeCheck
type UptimeCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UptimeCheck `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UptimeCheck{}, &UptimeCheckList{})
}
