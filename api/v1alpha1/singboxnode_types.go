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

// ProtocolConfig declares a supported inbound protocol and its host port
type ProtocolConfig struct {
	// Protocol is the proxy protocol: hysteria2, vless, trojan, socks5, or http
	// +kubebuilder:validation:Enum=hysteria2;vless;trojan;socks5;http
	Protocol string `json:"protocol"`
	// Port is the port on the host machine for client connections (1-65535).
	// This is exposed via hostPort on the pod, so the same port number can be
	// used on different physical nodes without conflict.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// SingBoxNodeSpec defines the desired state of SingBoxNode
type SingBoxNodeSpec struct {
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
	// RelayPort is the host port for inter-node relay connections.
	// Required for outbound nodes that receive relay traffic from inbound nodes.
	// When unset, outbound nodes are excluded from generated configs.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	RelayPort int32 `json:"relayPort,omitempty"`
	// TLSSecretName overrides the default TLS secret for this node.
	// When set, the named kubernetes.io/tls Secret is mounted and used for all
	// TLS-requiring protocols (e.g. hysteria2). Falls back to the operator-wide
	// default (configurable via --default-tls-secret flag, default: sing-box-tls).
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`
}

// SingBoxNodeStatus defines the observed state of SingBoxNode
type SingBoxNodeStatus struct {
	// Phase is the current lifecycle phase: Pending, Running, Failed
	// +optional
	Phase string `json:"phase,omitempty"`
	// ConfigHash is the sha256[:16] of the current sing-box config
	// +optional
	ConfigHash string `json:"configHash,omitempty"`
	// EntryEndpoints lists the external endpoints for inbound protocols
	// +optional
	EntryEndpoints []string `json:"entryEndpoints,omitempty"`
	// TLSServerName is the first DNS SAN extracted from the node's TLS certificate.
	// Used by clients as the SNI server_name for TLS-requiring protocols (e.g. hysteria2).
	// +optional
	TLSServerName string `json:"tlsServerName,omitempty"`
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
// +kubebuilder:resource:scope=Namespaced,shortName=sbn
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Roles",type=string,JSONPath=`.spec.roles`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SingBoxNode is the Schema for the singboxnodes API
type SingBoxNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SingBoxNodeSpec   `json:"spec,omitempty"`
	Status SingBoxNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SingBoxNodeList contains a list of SingBoxNode
type SingBoxNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SingBoxNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SingBoxNode{}, &SingBoxNodeList{})
}
