package webhook_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/webhook"
)

// ---------------------------------------------------------------------------
// TestUserGroupWebhook_ValidateCreate — validates UserGroup node name rules
// ---------------------------------------------------------------------------
func TestUserGroupWebhook_ValidateCreate(t *testing.T) {
	v := &webhook.UserGroupCustomValidator{}
	ctx := context.Background()

	t.Run("accepts valid UserGroup with valid node names", func(t *testing.T) {
		ug := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"node-a", "node-b"},
				DeniedNodes:  []string{"node-c"},
			},
		}
		_, err := v.ValidateCreate(ctx, ug)
		if err != nil {
			t.Errorf("expected no error for valid node names, got: %v", err)
		}
	})

	t.Run("rejects UserGroup with invalid node name (uppercase)", func(t *testing.T) {
		ug := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"UPPERCASE-NODE"},
			},
		}
		_, err := v.ValidateCreate(ctx, ug)
		if err == nil {
			t.Error("expected error for invalid allowedNodes name 'UPPERCASE-NODE', got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "allowedNodes") {
			t.Errorf("expected error to mention 'allowedNodes', got: %v", err)
		}
	})

	t.Run("accepts UserGroup with empty allowedNodes and deniedNodes", func(t *testing.T) {
		ug := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{},
		}
		_, err := v.ValidateCreate(ctx, ug)
		if err != nil {
			t.Errorf("expected no error for empty allowedNodes and deniedNodes, got: %v", err)
		}
	})

	t.Run("rejects UserGroup with invalid deniedNodes name (space)", func(t *testing.T) {
		ug := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				DeniedNodes: []string{"node with spaces"},
			},
		}
		_, err := v.ValidateCreate(ctx, ug)
		if err == nil {
			t.Error("expected error for invalid deniedNodes name 'node with spaces', got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "deniedNodes") {
			t.Errorf("expected error to mention 'deniedNodes', got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// TestUserGroupWebhook_ValidateUpdate — update delegates to same validation
// ---------------------------------------------------------------------------
func TestUserGroupWebhook_ValidateUpdate(t *testing.T) {
	v := &webhook.UserGroupCustomValidator{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"node-a"},
			},
		}
		newUG := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"INVALID_UPPERCASE"},
			},
		}
		_, err := v.ValidateUpdate(ctx, old, newUG)
		if err == nil {
			t.Error("expected error for invalid node name on update, got nil")
		}
	})

	t.Run("accepts valid update", func(t *testing.T) {
		old := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"node-a"},
			},
		}
		newUG := &v1alpha1.UserGroup{
			Spec: v1alpha1.UserGroupSpec{
				AllowedNodes: []string{"node-a", "node-b"},
				DeniedNodes:  []string{"node-c"},
			},
		}
		_, err := v.ValidateUpdate(ctx, old, newUG)
		if err != nil {
			t.Errorf("expected no error for valid update, got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// TestUserWebhook_UserGroupRef — validates userGroupRef DNS subdomain format
// ---------------------------------------------------------------------------
func TestUserWebhook_UserGroupRef(t *testing.T) {
	w := &webhook.UserWebhook{}
	ctx := context.Background()

	t.Run("accepts User with valid userGroupRef", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret:   corev1.SecretReference{Name: "my-secret"},
				UserGroupRef: "my-group",
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("expected no error for valid userGroupRef 'my-group', got: %v", err)
		}
	})

	t.Run("rejects User with invalid userGroupRef (contains space)", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret:   corev1.SecretReference{Name: "my-secret"},
				UserGroupRef: "My Group",
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err == nil {
			t.Error("expected error for invalid userGroupRef 'My Group' (contains space), got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "userGroupRef") {
			t.Errorf("expected error to mention 'userGroupRef', got: %v", err)
		}
	})

	t.Run("accepts User with empty userGroupRef", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret:   corev1.SecretReference{Name: "my-secret"},
				UserGroupRef: "",
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("expected no error for empty userGroupRef, got: %v", err)
		}
	})

	t.Run("accepts User with hyphenated userGroupRef", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret:   corev1.SecretReference{Name: "my-secret"},
				UserGroupRef: "team-alpha-group",
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("expected no error for valid userGroupRef 'team-alpha-group', got: %v", err)
		}
	})
}
