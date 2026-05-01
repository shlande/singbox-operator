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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProxyRouteSpec defines the desired state of ProxyRoute
type ProxyRouteSpec struct {
	// InboundNode is the name of the inbound ProxyNode (must have inbound role)
	// +kubebuilder:validation:MinLength=1
	InboundNode string `json:"inboundNode"`
	// OutboundNode is the name of the outbound ProxyNode (must have outbound role)
	// +kubebuilder:validation:MinLength=1
	OutboundNode string `json:"outboundNode"`
}

// ProxyRouteStatus defines the observed state of ProxyRoute
type ProxyRouteStatus struct {
	// ResolvedInboundNode is the confirmed inbound node name after validation
	// +optional
	ResolvedInboundNode string `json:"resolvedInboundNode,omitempty"`
	// ResolvedOutboundNode is the confirmed outbound node name after validation
	// +optional
	ResolvedOutboundNode string `json:"resolvedOutboundNode,omitempty"`
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
// +kubebuilder:resource:scope=Namespaced,shortName=pr
// +kubebuilder:printcolumn:name="InboundNode",type=string,JSONPath=`.spec.inboundNode`
// +kubebuilder:printcolumn:name="OutboundNode",type=string,JSONPath=`.spec.outboundNode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProxyRoute is the Schema for the proxyroutes API
type ProxyRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxyRouteSpec   `json:"spec,omitempty"`
	Status ProxyRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxyRouteList contains a list of ProxyRoute
type ProxyRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxyRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProxyRoute{}, &ProxyRouteList{})
}
