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
	"net"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	EgressPolicyActionRoute  = "route"
	EgressPolicyActionReject = "reject"
)

const (
	EgressPolicyReadyConditionType    = "Ready"
	EgressPolicyDegradedConditionType = "Degraded"
)

// EgressPolicySelector selects SingBoxNodes or ExternalOutbounds by labels and
// optionally by resource names. MatchLabels and MatchExpressions follow the
// Kubernetes LabelSelector semantics; MatchNames is an additional exact-name
// constraint.
type EgressPolicySelector struct {
	// MatchLabels is a map of {key,value} pairs. All pairs must match.
	// +optional
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	// MatchExpressions is a list of label selector requirements. All
	// requirements must match.
	// +optional
	MatchExpressions []metav1.LabelSelectorRequirement `json:"matchExpressions,omitempty"`
	// MatchNames limits matches to resources with one of these names.
	// +optional
	// +listType=set
	MatchNames []string `json:"matchNames,omitempty"`
}

// Valid reports whether the label-selector portion uses valid Kubernetes
// selector syntax.
func (s *EgressPolicySelector) Valid() error {
	_, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels:      s.MatchLabels,
		MatchExpressions: s.MatchExpressions,
	})
	return err
}

// Matches reports whether obj satisfies this selector.
func (s *EgressPolicySelector) Matches(obj metav1.Object) bool {
	if len(s.MatchNames) > 0 {
		matched := false
		for _, name := range s.MatchNames {
			if name == obj.GetName() {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels:      s.MatchLabels,
		MatchExpressions: s.MatchExpressions,
	})
	return err == nil && selector.Matches(labels.Set(obj.GetLabels()))
}

// EgressPolicyMatch defines the sing-box route rule match fields. Empty fields
// are omitted from the generated rule; at least one field is required unless
// the policy is marked fallback.
type EgressPolicyMatch struct {
	// Domain matches exact domain names.
	// +optional
	Domain []string `json:"domain,omitempty"`
	// DomainSuffix matches domain suffixes.
	// +optional
	DomainSuffix []string `json:"domainSuffix,omitempty"`
	// DomainRegex matches domains using a regular expression.
	// +optional
	DomainRegex []string `json:"domainRegex,omitempty"`
	// IPCIDR matches destination IP CIDRs.
	// +optional
	IPCIDR []string `json:"ipCIDR,omitempty"`
	// RuleSet references a sing-box rule-set tag.
	// +optional
	RuleSet []string `json:"ruleSet,omitempty"`
}

// Valid reports whether the match fields are syntactically valid. The
// fallback argument permits an empty match, which becomes a catch-all rule.
func (m *EgressPolicyMatch) Valid(fallback bool) error {
	if len(m.Domain) == 0 && len(m.DomainSuffix) == 0 && len(m.DomainRegex) == 0 && len(m.IPCIDR) == 0 && len(m.RuleSet) == 0 {
		if fallback {
			return nil
		}
		return errEmptyPolicyMatch{}
	}
	for _, expression := range m.DomainRegex {
		if _, err := regexp.Compile(expression); err != nil {
			return err
		}
	}
	for _, cidr := range m.IPCIDR {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return err
		}
	}
	return nil
}

type errEmptyPolicyMatch struct{}

func (errEmptyPolicyMatch) Error() string {
	return "at least one match field is required unless fallback is true"
}

// EgressPolicySpec defines a routing policy applied to selected ingress nodes.
type EgressPolicySpec struct {
	// IngressSelector selects inbound SingBoxNodes that receive this policy.
	IngressSelector EgressPolicySelector `json:"ingressSelector"`
	// EgressSelector selects exactly one outbound SingBoxNode or ExternalOutbound
	// for a route action. It is ignored for reject actions.
	// +optional
	EgressSelector EgressPolicySelector `json:"egressSelector,omitempty"`
	// Match contains destination matching criteria.
	Match EgressPolicyMatch `json:"match"`
	// Action is either route or reject.
	// +kubebuilder:validation:Enum=route;reject
	Action string `json:"action"`
	// Priority orders rules from lowest number to highest number. Ties are
	// resolved by policy name for deterministic output.
	// +optional
	Priority int32 `json:"priority,omitempty"`
	// FallbackAction controls traffic using the selected egress that did not
	// match this policy. Currently only reject is supported.
	// +kubebuilder:validation:Enum=reject
	// +optional
	FallbackAction string `json:"fallbackAction,omitempty"`
}

// EgressPolicyStatus defines the observed state of EgressPolicy.
type EgressPolicyStatus struct {
	// ResolvedIngresses contains the names of matching inbound nodes.
	// +optional
	// +listType=set
	ResolvedIngresses []string `json:"resolvedIngresses,omitempty"`
	// ResolvedEgress is set when exactly one egress target was selected.
	// +optional
	ResolvedEgress string `json:"resolvedEgress,omitempty"`
	// Conditions represent the latest available observations.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the policy generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ep
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type EgressPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressPolicySpec   `json:"spec,omitempty"`
	Status EgressPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type EgressPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EgressPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressPolicy{}, &EgressPolicyList{})
}
