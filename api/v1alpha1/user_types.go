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

// UserSpec defines the desired state of User
type UserSpec struct {
	// AuthSecret references the Secret containing authentication credentials
	// (e.g., password for hysteria2/trojan, uuid for vless)
	AuthSecret corev1.SecretReference `json:"authSecret"`
	// UserGroupRef is the name of the UserGroup in the same namespace.
	// If empty, no node restrictions apply to this user.
	// If set, the user is subject to the allowedNodes/deniedNodes rules of the referenced UserGroup.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	UserGroupRef string `json:"userGroupRef,omitempty"`
}

// UserStatus defines the observed state of User
type UserStatus struct {
	// ActiveNodeCount is the number of inbound SingBoxNodes this user is injected into
	// +optional
	ActiveNodeCount int32 `json:"activeNodeCount,omitempty"`
	// ActiveNodes lists the names of inbound SingBoxNodes this user is active on
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
// +kubebuilder:resource:scope=Namespaced,shortName=u
// +kubebuilder:printcolumn:name="ActiveNodes",type=integer,JSONPath=`.status.activeNodeCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// User is the Schema for the users API
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec,omitempty"`
	Status UserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
