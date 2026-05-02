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
	w := &webhook.SingBoxNodeWebhook{}
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
	w := &webhook.SingBoxNodeWebhook{}
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

	t.Run("rejects relayNodePort outside NodePort range", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef:       "node-1",
				Address:       "1.2.3.4",
				Region:        "us-west",
				Roles:         []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
				RelayNodePort: 1234,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for relayNodePort=1234, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "relayNodePort") {
			t.Errorf("Expected error to mention 'relayNodePort', got: %v", err)
		}
	})

	t.Run("accepts zero relayNodePort (random assignment)", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef:       "node-1",
				Address:       "1.2.3.4",
				Region:        "us-west",
				Roles:         []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
				RelayNodePort: 0,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for relayNodePort=0 (unset), got: %v", err)
		}
	})

	t.Run("accepts valid relayNodePort in range", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef:       "node-1",
				Address:       "1.2.3.4",
				Region:        "us-west",
				Roles:         []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
				RelayNodePort: 31962,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for relayNodePort=31962, got: %v", err)
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
					{Protocol: "vless", Port: 30443},
					{Protocol: "vless", Port: 30444},
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
					{Protocol: "vless", Port: 30443},
					{Protocol: "trojan", Port: 30443},
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

	t.Run("rejects supportedProtocols port below NodePort range", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 29999},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for port below 30000, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "30000") {
			t.Errorf("Expected error to mention '30000', got: %v", err)
		}
	})

	t.Run("rejects supportedProtocols port above NodePort range", func(t *testing.T) {
		node := &v1alpha1.SingBoxNode{
			Spec: v1alpha1.SingBoxNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 32768},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for port above 32767, got nil")
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
					{Protocol: "vless", Port: 30443},
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
	w := &webhook.SingBoxNodeWebhook{}
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

	t.Run("defaults empty protocol to hysteria2", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		if err := w.Default(ctx, user); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if user.Spec.Protocol != "hysteria2" {
			t.Errorf("Expected protocol=hysteria2, got %q", user.Spec.Protocol)
		}
	})

	t.Run("does not override explicitly set protocol", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "vless",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		if err := w.Default(ctx, user); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if user.Spec.Protocol != "vless" {
			t.Errorf("Expected protocol=vless (unchanged), got %q", user.Spec.Protocol)
		}
	})
}

func TestUserWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.UserWebhook{}
	ctx := context.Background()

	t.Run("accepts empty protocol (defaulted by webhook)", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for empty protocol (optional field), got: %v", err)
		}
	})

	t.Run("rejects unknown protocol", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "shadowsocks",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err == nil {
			t.Error("Expected error for unknown protocol, got nil")
		}
	})

	t.Run("rejects empty authSecret name", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "vless",
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
				Protocol:   "hysteria2",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for hysteria2 User, got: %v", err)
		}
	})

	t.Run("accepts valid User with vless", func(t *testing.T) {
		user := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "vless",
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
				Protocol:   "trojan",
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
				Protocol:   "socks5",
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
				Protocol:   "http",
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
				Protocol:   "vless",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		newUser := &v1alpha1.User{
			Spec: v1alpha1.UserSpec{
				Protocol:   "unknown-protocol",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateUpdate(ctx, old, newUser)
		if err == nil {
			t.Error("Expected error for unknown protocol on update, got nil")
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
