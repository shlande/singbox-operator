package apiserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func missingEgressPolicy() *v1alpha1.EgressPolicy {
	return &v1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ai-egress", Namespace: "default"},
		Spec: v1alpha1.EgressPolicySpec{
			IngressSelector: v1alpha1.EgressPolicySelector{MatchNames: []string{"in"}},
			EgressSelector:  v1alpha1.EgressPolicySelector{MatchNames: []string{"external"}},
			Match:           v1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}},
			Action:          v1alpha1.EgressPolicyActionRoute,
			FallbackAction:  v1alpha1.EgressPolicyActionReject,
		},
	}
}

func TestClientPolicyMissingEgressIsInactive(t *testing.T) {
	for _, selector := range []v1alpha1.EgressPolicySelector{
		{MatchNames: []string{"external"}},
		{MatchLabels: map[string]string{"tier": "missing"}},
	} {
		p := missingEgressPolicy()
		p.Spec.EgressSelector = selector
		input := ClientConfigInput{EgressPolicies: []*v1alpha1.EgressPolicy{p}, OutboundsByName: map[string]*v1alpha1.SingBoxNode{"other": makeOutboundNode("other", "us")}}
		outbounds, rules, targets, err := buildClientPolicies(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(outbounds) != 0 || len(rules) != 0 || len(targets) != 0 {
			t.Fatalf("inactive policy emitted outbounds=%v rules=%v targets=%v", outbounds, rules, targets)
		}
	}
}

func TestMergeClientConfigIgnoresInactivePolicyRuleSets(t *testing.T) {
	p := missingEgressPolicy()
	p.Spec.Match.RuleSet = []string{"undefined"}
	input := ClientConfigInput{EgressPolicies: []*v1alpha1.EgressPolicy{p}}
	for _, template := range [][]byte{DefaultTemplate, []byte(`{"route":{}}`)} {
		generated, err := BuildClientConfig(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := MergeClientConfig(template, generated, input)
		if err != nil {
			t.Fatal(err)
		}
		inputWithoutPolicy := input
		inputWithoutPolicy.EgressPolicies = nil
		want, err := MergeClientConfig(template, generated, inputWithoutPolicy)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("inactive policy changed config: got %s, want %s", got, want)
		}
	}
	// Once the target exists, rule_set validation must still run.
	input.ExternalOutboundsByName = map[string]*v1alpha1.ExternalOutbound{"external": {ObjectMeta: metav1.ObjectMeta{Name: "external"}}}
	if _, err := MergeClientConfig(DefaultTemplate, nil, input); err == nil {
		t.Fatal("active policy with undefined rule_set should fail")
	}
}

func TestClientPolicyAmbiguousEgressStillFails(t *testing.T) {
	p := missingEgressPolicy()
	p.Spec.EgressSelector.MatchNames = []string{"out", "external"}
	input := ClientConfigInput{
		EgressPolicies:          []*v1alpha1.EgressPolicy{p},
		OutboundsByName:         map[string]*v1alpha1.SingBoxNode{"out": makeOutboundNode("out", "us")},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{"external": {ObjectMeta: metav1.ObjectMeta{Name: "external"}}},
	}
	if _, _, _, err := buildClientPolicies(input); err == nil {
		t.Fatal("ambiguous selector must still fail")
	}
}

func TestHandlerPolicySurvivesEgressDeletionAndRecreation(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	user := makeUser("user", "auth")
	user.Namespace = "default"
	inbound := makeInboundNode("in", "us", "1.2.3.4", []v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 443}})
	inbound.Namespace = "default"
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:443"}
	external := &v1alpha1.ExternalOutbound{
		ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "default"},
		Spec:       v1alpha1.ExternalOutboundSpec{Region: "us", ClientGroups: []string{"ai"}},
	}
	p := missingEgressPolicy()
	c := newFakeClient(user, makeUserSecret("default", "auth", testUUID, ""), inbound, external, p)
	srv := &Server{Client: c}
	check := func(active bool) {
		t.Helper()
		w := httptest.NewRecorder()
		srv.handleClientConfig(w, httptest.NewRequest(http.MethodGet, "/api/v1/client-config/default/"+testUUID, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
		}
		if got := bytes.Contains(w.Body.Bytes(), []byte("example.com")); got != active {
			t.Fatalf("policy active=%v, want %v: %s", got, active, w.Body.String())
		}
	}
	check(true)
	if err := c.Delete(context.Background(), external); err != nil {
		t.Fatal(err)
	}
	check(false)
	external.ResourceVersion = ""
	if err := c.Create(context.Background(), external); err != nil {
		t.Fatal(err)
	}
	check(true)
}

func TestClientRejectPolicyDoesNotRequireEgress(t *testing.T) {
	p := missingEgressPolicy()
	p.Spec.Action = v1alpha1.EgressPolicyActionReject
	_, rules, _, err := buildClientPolicies(ClientConfigInput{EgressPolicies: []*v1alpha1.EgressPolicy{p}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].(map[string]any)["action"] != v1alpha1.EgressPolicyActionReject {
		t.Fatalf("explicit reject policy should still apply: %v", rules)
	}
}
