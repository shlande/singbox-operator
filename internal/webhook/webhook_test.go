package webhook_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/webhook"
)

func TestSingBoxNodeWebhook_Default(t *testing.T) {
	w := &webhook.SingBoxNodeWebhook{NodePortRangeMin: 30000, NodePortRangeMax: 32767}
	ctx := context.Background()

	t.Run("does not override explicitly set protocol port", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 19000},
				},
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.SupportedProtocols[0].Port != 19000 {
			t.Errorf("Expected vless port=19000 (unchanged), got %d", node.Spec.SupportedProtocols[0].Port)
		}
	})
}

func TestSingBoxNodeWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.SingBoxNodeWebhook{NodePortRangeMin: 30000, NodePortRangeMax: 32767}
	ctx := context.Background()

	t.Run("rejects empty address", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for empty address, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "address") {
			t.Errorf("Expected error to mention 'address', got: %v", err)
		}
	})

	t.Run("rejects invalid address with spaces", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "invalid host",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for address with spaces, got nil")
		}
	})

	t.Run("accepts relayPort outside NodePort range", func(t *testing.T) {
		for _, port := range []int32{0, 1234, 10808, 29999} {
			node := &v1alpha1.SingBoxNode{
				Spec: v1alpha1.SingBoxNodeSpec{
					NodeRef:   "node-1",
					Address:   "1.2.3.4",
					Region:    "us-west",
					Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
					RelayPort: port,
				},
			}
			_, err := w.ValidateCreate(ctx, node)
			if err != nil {
				t.Errorf("Expected no error for relayPort=%d, got: %v", port, err)
			}
		}
	})

	t.Run("rejects relayPort inside NodePort range", func(t *testing.T) {
		for _, port := range []int32{30000, 31962, 32767} {
			node := &v1alpha1.SingBoxNode{
				Spec: v1alpha1.SingBoxNodeSpec{
					NodeRef:   "node-1",
					Address:   "1.2.3.4",
					Region:    "us-west",
					Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
					RelayPort: port,
				},
			}
			_, err := w.ValidateCreate(ctx, node)
			if err == nil {
				t.Errorf("Expected error for relayPort=%d (in NodePort range), got nil", port)
			}
		}
	})

	t.Run("rejects supportedProtocols port inside NodePort range", func(t *testing.T) {
		for _, port := range []int32{30000, 30080, 32767} {
			node := &v1alpha1.SingBoxNode{
				Spec: v1alpha1.SingBoxNodeSpec{
					NodeRef: "node-1",
					Address: "1.2.3.4",
					Region:  "us-west",
					Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
					SupportedProtocols: []v1alpha1.ProtocolConfig{
						{Protocol: "vless", Port: port},
					},
				},
			}
			_, err := w.ValidateCreate(ctx, node)
			if err == nil {
				t.Errorf("Expected error for port=%d (in NodePort range), got nil", port)
			}
		}
	})

	t.Run("rejects duplicate protocols", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 8443},
					{Protocol: "vless", Port: 8444},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for duplicate protocol, got nil")
		}
	})

	t.Run("rejects port conflict between two supportedProtocols", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 8443},
					{Protocol: "trojan", Port: 8443},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for port conflict, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "conflict") {
			t.Errorf("Expected error to mention 'conflict', got: %v", err)
		}
	})

	t.Run("rejects port conflict between two supportedProtocols", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
					{Protocol: "trojan", Port: 10443},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for port conflict between protocols, got nil")
		}
	})

	t.Run("rejects empty roles", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for empty roles, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "roles") {
			t.Errorf("Expected error to mention 'roles', got: %v", err)
		}
	})

	t.Run("rejects invalid role", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{"relay"},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for invalid role, got nil")
		}
	})

	t.Run("accepts supportedProtocols port outside NodePort range", func(t *testing.T) {
		for _, port := range []int32{443, 8080, 29999, 32768, 65535} {
			node := &v1alpha1.SingBoxNode{
				Spec: v1alpha1.SingBoxNodeSpec{
					NodeRef: "node-1",
					Address: "1.2.3.4",
					Region:  "us-west",
					Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
					SupportedProtocols: []v1alpha1.ProtocolConfig{
						{Protocol: "vless", Port: port},
					},
				},
			}
			_, err := w.ValidateCreate(ctx, node)
			if err != nil {
				t.Errorf("Expected no error for port=%d, got: %v", port, err)
			}
		}
	})

	t.Run("accepts valid SingBoxNode with IP", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 8443},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for valid SingBoxNode, got: %v", err)
		}
	})

	t.Run("accepts valid SingBoxNode with hostname", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "example.com",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for valid SingBoxNode with hostname, got: %v", err)
		}
	})

	t.Run("accepts both inbound and outbound roles", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for inbound+outbound roles, got: %v", err)
		}
	})
}

func TestSingBoxNodeWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.SingBoxNodeWebhook{NodePortRangeMin: 30000, NodePortRangeMax: 32767}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			},
		}
		newNode := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			},
		}
		_, err := w.ValidateUpdate(ctx, old, newNode)
		if err == nil {
			t.Error("Expected error for empty address on update, got nil")
		}
	})
}

func TestUserWebhook_Default(t *testing.T) {
	w := &webhook.UserWebhook{}
	ctx := context.Background()

	t.Run("Default() returns no error for user with authSecret", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		if err := w.Default(ctx, user); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
	})

	t.Run("Default() does not modify authSecret", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		if err := w.Default(ctx, user); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if user.Spec.AuthSecret.Name != "my-secret" {
			t.Errorf("Expected authSecret.name=my-secret (unchanged), got %q", user.Spec.AuthSecret.Name)
		}
	})
}

func TestUserWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.UserWebhook{}
	ctx := context.Background()

	t.Run("accepts User with valid authSecret", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for valid authSecret, got: %v", err)
		}
	})

	t.Run("rejects invalid userGroupRef (contains space)", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret:   corev1.SecretReference{Name: "my-secret"},
				UserGroupRef: "my group",
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err == nil {
			t.Error("Expected error for invalid userGroupRef, got nil")
		}
	})

	t.Run("rejects empty authSecret name", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: ""},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err == nil {
			t.Error("Expected error for empty authSecret.name, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "authSecret") {
			t.Errorf("Expected error to mention 'authSecret', got: %v", err)
		}
	})

	t.Run("accepts valid User with hysteria2", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for User, got: %v", err)
		}
	})

	t.Run("accepts valid User with vless", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for valid User, got: %v", err)
		}
	})

	t.Run("accepts valid User with trojan", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for trojan User, got: %v", err)
		}
	})

	t.Run("accepts valid User with socks5", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for socks5 User, got: %v", err)
		}
	})

	t.Run("accepts valid User with http", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for http User, got: %v", err)
		}
	})
}

func TestUserWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.UserWebhook{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		newUser := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				AuthSecret: corev1.SecretReference{Name: ""},
			},
		}
		_, err := w.ValidateUpdate(ctx, old, newUser)
		if err == nil {
			t.Error("Expected error for empty authSecret.name on update, got nil")
		}
	})
}

func TestCustomRouteWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.CustomRouteWebhook{}
	ctx := context.Background()

	t.Run("rejects empty inboundNode", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err == nil {
			t.Error("Expected error for empty inboundNode, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "inboundNode") {
			t.Errorf("Expected error to mention 'inboundNode', got: %v", err)
		}
	})

	t.Run("rejects empty outboundNode", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err == nil {
			t.Error("Expected error for empty outboundNode, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "outboundNode") {
			t.Errorf("Expected error to mention 'outboundNode', got: %v", err)
		}
	})

	t.Run("rejects both empty", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "",
				OutboundNode: "",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err == nil {
			t.Error("Expected error for both empty nodes, got nil")
		}
	})

	t.Run("accepts valid CustomRoute", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err != nil {
			t.Errorf("Expected no error for valid CustomRoute, got: %v", err)
		}
	})
}

func TestCustomRouteWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.CustomRouteWebhook{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		newRoute := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateUpdate(ctx, old, newRoute)
		if err == nil {
			t.Error("Expected error for empty inboundNode on update, got nil")
		}
	})
}

func TestCustomRouteWebhook_ValidateDelete(t *testing.T) {
	w := &webhook.CustomRouteWebhook{}
	ctx := context.Background()

	t.Run("allows delete", func(t *testing.T) {
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateDelete(ctx, route)
		if err != nil {
			t.Errorf("Expected no error for delete, got: %v", err)
		}
	})
}
