/*
Copyright 2026.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProxyUserSpec defines the desired state of ProxyUser
type ProxyUserSpec struct {
	// Protocol is the inbound proxy protocol (must match a ProxyNode's supportedProtocols).
	// Defaults to hysteria2 when omitted.
	// +kubebuilder:validation:Enum=hysteria2;vless;trojan;socks5;http
	// +kubebuilder:default=hysteria2
	// +optional
	Protocol string `json:"protocol,omitempty"`
	// AuthSecret references the Secret containing authentication credentials
	// (e.g., password for hysteria2/trojan, uuid for vless)
	AuthSecret corev1.SecretReference `json:"authSecret"`
}

// ProxyUserStatus defines the observed state of ProxyUser
type ProxyUserStatus struct {
	// ActiveNodeCount is the number of inbound ProxyNodes this user is injected into
	// +optional
	ActiveNodeCount int32 `json:"activeNodeCount,omitempty"`
	// ActiveNodes lists the names of inbound ProxyNodes this user is active on
	// +optional
	ActiveNodes []string `json:"activeNodes,omitempty"`
	// Conditions represent the latest available observations
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the generation observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pu
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.spec.protocol`
// +kubebuilder:printcolumn:name="ActiveNodes",type=integer,JSONPath=`.status.activeNodeCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProxyUser is the Schema for the proxyusers API
type ProxyUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxyUserSpec   `json:"spec,omitempty"`
	Status ProxyUserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxyUserList contains a list of ProxyUser
type ProxyUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxyUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProxyUser{}, &ProxyUserList{})
}
