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

// CustomRouteSpec defines the desired state of CustomRoute
type CustomRouteSpec struct {
	// InboundNode is the name of the inbound SingBoxNode (must have inbound role)
	// +kubebuilder:validation:MinLength=1
	InboundNode string `json:"inboundNode"`
	// OutboundNode is the name of the outbound SingBoxNode (must have outbound role)
	// +kubebuilder:validation:MinLength=1
	OutboundNode string `json:"outboundNode"`
}

// CustomRouteStatus defines the observed state of CustomRoute
type CustomRouteStatus struct {
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
// +kubebuilder:resource:scope=Namespaced,shortName=cr
// +kubebuilder:printcolumn:name="InboundNode",type=string,JSONPath=`.spec.inboundNode`
// +kubebuilder:printcolumn:name="OutboundNode",type=string,JSONPath=`.spec.outboundNode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CustomRoute is the Schema for the customroutes API
type CustomRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CustomRouteSpec   `json:"spec,omitempty"`
	Status CustomRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CustomRouteList contains a list of CustomRoute
type CustomRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CustomRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CustomRoute{}, &CustomRouteList{})
}
