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

// UserGroupSpec defines the desired state of UserGroup.
type UserGroupSpec struct {
	// AllowedNodes is the whitelist of SingBoxNode names (metadata.name) this group may use.
	// If empty, all nodes are allowed (subject to DeniedNodes).
	// Deny-wins: if a node appears in both AllowedNodes and DeniedNodes, it is denied.
	// +listType=set
	// +optional
	AllowedNodes []string `json:"allowedNodes,omitempty"`

	// DeniedNodes is the blacklist of SingBoxNode names (metadata.name) this group may NOT use.
	// Deny-wins: if a node appears in both AllowedNodes and DeniedNodes, it is denied.
	// Restriction is node-level: a denied node cannot be used as either an inbound or outbound.
	// +listType=set
	// +optional
	DeniedNodes []string `json:"deniedNodes,omitempty"`
}

// UserGroupStatus defines the observed state of UserGroup.
type UserGroupStatus struct {
	// ObservedGeneration is the generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the UserGroup state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// UserGroup is the Schema for the usergroups API.
// A UserGroup restricts which SingBoxNodes its member Users can access.
// Users bind to a UserGroup via spec.userGroupRef. If a node is restricted,
// the user cannot use it as either an inbound or an outbound.
type UserGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of UserGroup
	// +optional
	Spec UserGroupSpec `json:"spec,omitempty"`

	// status defines the observed state of UserGroup
	// +optional
	Status UserGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UserGroupList contains a list of UserGroup
type UserGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UserGroup{}, &UserGroupList{})
}
