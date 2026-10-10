package controller

import (
	"context"
	"testing"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveEgressPolicyNoMatchesIsInactive(t *testing.T) {
	p := &v1alpha1.EgressPolicy{Spec: v1alpha1.EgressPolicySpec{
		Action:         v1alpha1.EgressPolicyActionRoute,
		EgressSelector: v1alpha1.EgressPolicySelector{MatchNames: []string{"missing"}},
		Match:          v1alpha1.EgressPolicyMatch{Domain: []string{"example.com"}},
		FallbackAction: v1alpha1.EgressPolicyActionReject,
	}}
	matches, name, message := resolveEgressPolicy(p, nil, nil)
	if len(matches) != 0 || name != "" || message != "" {
		t.Fatalf("expected inactive policy without error, got matches=%v name=%q message=%q", matches, name, message)
	}
}

func TestCollectInputUnmatchedPolicyIgnoresStaleResolvedEgress(t *testing.T) {
	in := &v1alpha1.SingBoxNode{
		ObjectMeta: metav1.ObjectMeta{Name: "in", Namespace: "default"},
		Spec:       v1alpha1.SingBoxNodeSpec{Region: "us", Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound}},
	}
	out := &v1alpha1.ExternalOutbound{
		ObjectMeta: metav1.ObjectMeta{Name: "out", Namespace: "default"},
		Spec:       v1alpha1.ExternalOutboundSpec{Region: "us"},
	}
	p := &v1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ai", Namespace: "default"},
		Spec: v1alpha1.EgressPolicySpec{
			Action:         v1alpha1.EgressPolicyActionRoute,
			EgressSelector: v1alpha1.EgressPolicySelector{MatchLabels: map[string]string{"tier": "ai"}},
			Match:          v1alpha1.EgressPolicyMatch{Domain: []string{"example.com"}},
			FallbackAction: v1alpha1.EgressPolicyActionReject,
		},
		Status: v1alpha1.EgressPolicyStatus{ResolvedEgress: "out"},
	}
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(in, out, p).Build()
	r := &SingBoxNodeReconciler{Client: c, Scheme: scheme}
	input, err := r.collectInput(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.EgressPolicies) != 0 {
		t.Fatal("unmatched policy must not be activated by stale ResolvedEgress")
	}
}

func TestEgressPolicyMissingTargetStatusAndRecovery(t *testing.T) {
	p := &v1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ai-egress", Namespace: "default", Generation: 1},
		Spec: v1alpha1.EgressPolicySpec{
			Action: v1alpha1.EgressPolicyActionRoute,
			Match:  v1alpha1.EgressPolicyMatch{Domain: []string{"example.com"}},
		},
		Status: v1alpha1.EgressPolicyStatus{ResolvedEgress: "deleted"},
	}
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(p).WithObjects(p).Build()
	r := &EgressPolicyReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()
	for _, tc := range []struct {
		name            string
		egresses        []string
		ready, degraded metav1.ConditionStatus
		reason          string
	}{
		{"missing", nil, metav1.ConditionFalse, metav1.ConditionFalse, "EgressNotFound"},
		{"restored", []string{"out"}, metav1.ConditionTrue, metav1.ConditionFalse, "Resolved"},
		{"ambiguous", []string{"out", "other"}, metav1.ConditionFalse, metav1.ConditionTrue, "EgressNotUnique"},
		{"deleted again", nil, metav1.ConditionFalse, metav1.ConditionFalse, "EgressNotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := validatePolicyResolution(p, []string{"in"}, tc.egresses)
			if err := r.updateStatus(ctx, p, []string{"in"}, tc.egresses, reason, message); err != nil {
				t.Fatal(err)
			}
			var got v1alpha1.EgressPolicy
			if err := c.Get(ctx, types.NamespacedName{Name: p.Name, Namespace: p.Namespace}, &got); err != nil {
				t.Fatal(err)
			}
			ready := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.EgressPolicyReadyConditionType)
			degraded := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.EgressPolicyDegradedConditionType)
			if ready == nil || ready.Status != tc.ready || ready.Reason != tc.reason || degraded == nil || degraded.Status != tc.degraded {
				t.Fatalf("unexpected conditions: %+v", got.Status.Conditions)
			}
			if len(tc.egresses) != 1 && got.Status.ResolvedEgress != "" {
				t.Fatalf("stale resolved egress: %q", got.Status.ResolvedEgress)
			}
		})
	}
}
