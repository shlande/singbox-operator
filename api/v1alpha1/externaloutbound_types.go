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

// ExternalOutboundAcceptedConditionType is the condition type that reflects whether
// the ExternalOutbound spec is valid (TLS rules, credentials secret present and
// complete, no name conflict with SingBoxNode).
const ExternalOutboundAcceptedConditionType = "Accepted"

// Protocol names used in credential resolution, kept in sync with
// ExternalOutboundProtocol's validation enum.
const (
	ExternalProtocolSocks5      = "socks5"
	ExternalProtocolHTTP        = "http"
	ExternalProtocolShadowsocks = "shadowsocks"
	ExternalProtocolTrojan      = "trojan"
	ExternalProtocolHysteria2   = "hysteria2"
	ExternalProtocolTUIC        = "tuic"
	ExternalProtocolAnyTLS      = "anytls"
)

// Secret data keys consumed from spec.credentialsSecretRef.
const (
	CredKeyUsername     = "username"
	CredKeyPassword     = "password"
	CredKeyMethod       = "method"
	CredKeyUUID         = "uuid"
	CredKeyObfsPassword = "obfsPassword"
)

// ExternalOutboundProtocol is the proxy protocol the external server speaks.
// +kubebuilder:validation:Enum=socks5;http;shadowsocks;trojan;hysteria2;tuic;anytls
type ExternalOutboundProtocol string

// ExternalOutboundTLS configures TLS for protocols that require it.
type ExternalOutboundTLS struct {
	// ServerName is the TLS SNI sent to the server.
	// Required unless insecure is true.
	// +optional
	ServerName string `json:"serverName,omitempty"`
	// Insecure skips certificate verification (self-signed / bare-IP setups).
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}

// Hysteria2Options holds hysteria2-specific tuning.
// Only meaningful when protocol is hysteria2.
type Hysteria2Options struct {
	// UpMbps declares upload bandwidth; 0 or unset enables BBR auto mode.
	// +kubebuilder:validation:Minimum=0
	// +optional
	UpMbps int64 `json:"upMbps,omitempty"`
	// DownMbps declares download bandwidth; 0 or unset enables BBR auto mode.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DownMbps int64 `json:"downMbps,omitempty"`
	// Obfs enables salamander obfuscation; the password is read from the
	// obfsPassword key of the credentials secret.
	// +optional
	Obfs bool `json:"obfs,omitempty"`
}

// ExternalOutboundSpec defines an outbound proxy server that is deployed and
// managed outside the cluster by the user. The operator never creates workloads
// for it; it only renders it into inbound nodes' sing-box configs.
type ExternalOutboundSpec struct {
	// Protocol is the outbound protocol the external server speaks.
	Protocol ExternalOutboundProtocol `json:"protocol"`
	// Server is the IP or hostname of the external proxy server.
	// +kubebuilder:validation:MinLength=1
	Server string `json:"server"`
	// Port is the listening port of the external proxy server.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// CredentialsSecretRef points to a Secret in the same namespace holding the
	// static credentials configured on the external server. Expected keys by
	// protocol:
	//   socks5/http:     username, password (both optional)
	//   shadowsocks:     method, password
	//   trojan/hysteria2/anytls: password
	//   tuic:            uuid, password
	//   hysteria2 obfs:  obfsPassword
	// May be omitted only for socks5/http (no authentication).
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
	// TLS settings. Required for trojan, hysteria2, tuic and anytls; optional for
	// http; forbidden for socks5 and shadowsocks.
	// +optional
	TLS *ExternalOutboundTLS `json:"tls,omitempty"`
	// Hysteria2 holds hysteria2-specific tuning. Only meaningful when protocol
	// is hysteria2.
	// +optional
	Hysteria2 *Hysteria2Options `json:"hysteria2,omitempty"`
	// Region mirrors SingBoxNode.spec.region semantics: when non-empty, inbound
	// nodes in the same region auto-discover this outbound. When empty, the
	// outbound is only usable via explicit CustomRoute bindings.
	// +optional
	Region string `json:"region,omitempty"`
	// ClientRegion overrides the region label used for client config grouping.
	// When empty, client configs group this outbound by spec.region. This field
	// only affects client-side selector groups; server-side route discovery
	// always uses spec.region.
	// +optional
	ClientRegion string `json:"clientRegion,omitempty"`
	// AllowedInbounds restricts which inbound SingBoxNodes may use this outbound.
	// Empty means allow all. Mirrors SingBoxNode.spec.allowedInbounds.
	// +optional
	// +listType=set
	AllowedInbounds []string `json:"allowedInbounds,omitempty"`
}

// ExternalOutboundStatus defines the observed state of ExternalOutbound.
type ExternalOutboundStatus struct {
	// Conditions represent the latest available observations.
	// The "Accepted" condition reports spec validity.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=eob
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.spec.protocol`
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.server`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=='Accepted')].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExternalOutbound is the Schema for the externaloutbounds API.
type ExternalOutbound struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalOutboundSpec   `json:"spec,omitempty"`
	Status ExternalOutboundStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ExternalOutboundList contains a list of ExternalOutbound.
type ExternalOutboundList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalOutbound `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalOutbound{}, &ExternalOutboundList{})
}
