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

// ProxyRole defines the role of a proxy node
// +kubebuilder:validation:Enum=inbound;outbound
type ProxyRole string

const (
	ProxyRoleInbound  ProxyRole = "inbound"
	ProxyRoleOutbound ProxyRole = "outbound"
)

// ProtocolConfig declares a supported inbound protocol and its external NodePort
type ProtocolConfig struct {
	// Protocol is the proxy protocol: vless, trojan, socks5, or http
	// +kubebuilder:validation:Enum=vless;trojan;socks5;http
	Protocol string `json:"protocol"`
	// Port is the external NodePort for client connections
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	Port int32 `json:"port"`
}

// ProxyNodeSpec defines the desired state of ProxyNode
type ProxyNodeSpec struct {
	// NodeRef is the name of the Kubernetes Node this proxy runs on
	// +kubebuilder:validation:MinLength=1
	NodeRef string `json:"nodeRef"`
	// Address is the public IP or hostname of the host machine.
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`
	// Region is the geographic region label (e.g. "us-west")
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`
	// Roles defines the proxy roles: inbound and/or outbound (no relay)
	// +kubebuilder:validation:MinItems=1
	Roles []ProxyRole `json:"roles"`
	// SupportedProtocols declares inbound protocols (only meaningful for inbound role)
	// +optional
	SupportedProtocols []ProtocolConfig `json:"supportedProtocols,omitempty"`
	// RelayNodePort is the Kubernetes NodePort for inter-node relay connections (30000-32767).
	// Required for outbound nodes that receive relay traffic from inbound nodes.
	// When unset, outbound nodes are excluded from generated configs.
	// +optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	RelayNodePort int32 `json:"relayNodePort,omitempty"`
}

// ProxyNodeStatus defines the observed state of ProxyNode
type ProxyNodeStatus struct {
	// Phase is the current lifecycle phase: Pending, Running, Failed
	// +optional
	Phase string `json:"phase,omitempty"`
	// ConfigHash is the sha256[:16] of the current sing-box config
	// +optional
	ConfigHash string `json:"configHash,omitempty"`
	// EntryEndpoints lists the external endpoints for inbound protocols
	// +optional
	EntryEndpoints []string `json:"entryEndpoints,omitempty"`
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
// +kubebuilder:resource:scope=Namespaced,shortName=pn
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Roles",type=string,JSONPath=`.spec.roles`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProxyNode is the Schema for the proxynodes API
type ProxyNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxyNodeSpec   `json:"spec,omitempty"`
	Status ProxyNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxyNodeList contains a list of ProxyNode
type ProxyNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxyNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProxyNode{}, &ProxyNodeList{})
}
