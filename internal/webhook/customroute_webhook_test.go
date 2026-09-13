package webhook_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/webhook"
)

// ---------------------------------------------------------------------------
// TestCustomRouteWebhook_OutboundKind — validates spec.outboundKind enum check
// ---------------------------------------------------------------------------
func TestCustomRouteWebhook_OutboundKind(t *testing.T) {
	w := &webhook.CustomRouteWebhook{}
	ctx := context.Background()

	t.Run("accepts empty outboundKind (defaults to SingBoxNode)", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err != nil {
			t.Errorf("expected no error for empty outboundKind, got: %v", err)
		}
	})

	t.Run("accepts SingBoxNode outboundKind", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
				OutboundKind: v1alpha1.OutboundKindSingBoxNode,
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err != nil {
			t.Errorf("expected no error for outboundKind=SingBoxNode, got: %v", err)
		}
	})

	t.Run("accepts ExternalOutbound outboundKind", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "external-1",
				OutboundKind: v1alpha1.OutboundKindExternalOutbound,
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err != nil {
			t.Errorf("expected no error for outboundKind=ExternalOutbound, got: %v", err)
		}
	})

	t.Run("rejects unknown outboundKind on create", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
				OutboundKind: "Bogus",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err == nil {
			t.Error("expected error for unknown outboundKind, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "outboundKind") {
			t.Errorf("expected error to mention 'outboundKind', got: %v", err)
		}
	})

	t.Run("rejects unknown outboundKind on update", func(t *testing.T) {
		oldRoute := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		newRoute := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
				OutboundKind: "external", // wrong case, not a valid enum value
			},
		}
		_, err := w.ValidateUpdate(ctx, oldRoute, newRoute)
		if err == nil {
			t.Error("expected error for unknown outboundKind on update, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "outboundKind") {
			t.Errorf("expected error to mention 'outboundKind', got: %v", err)
		}
	})

	t.Run("accepts valid outboundKind on update", func(t *testing.T) {
		oldRoute := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		newRoute := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "external-1",
				OutboundKind: v1alpha1.OutboundKindExternalOutbound,
			},
		}
		_, err := w.ValidateUpdate(ctx, oldRoute, newRoute)
		if err != nil {
			t.Errorf("expected no error for valid outboundKind on update, got: %v", err)
		}
	})
}
