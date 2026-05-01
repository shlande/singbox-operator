package webhook_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/webhook"
)

func TestProxyNodeWebhook_Default(t *testing.T) {
	w := &webhook.ProxyNodeWebhook{}
	ctx := context.Background()

	t.Run("injects default relayPort", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 0,
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.RelayPort != 10808 {
			t.Errorf("Expected relayPort=10808, got %d", node.Spec.RelayPort)
		}
	})

	t.Run("injects default relayProtocol", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:       "node-1",
				Address:       "1.2.3.4",
				Region:        "us-west",
				Roles:         []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayProtocol: "",
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.RelayProtocol != "socks5" {
			t.Errorf("Expected relayProtocol=socks5, got %s", node.Spec.RelayProtocol)
		}
	})

	t.Run("injects default port for vless protocol", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 0},
				},
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.SupportedProtocols[0].Port != 10443 {
			t.Errorf("Expected vless port=10443, got %d", node.Spec.SupportedProtocols[0].Port)
		}
	})

	t.Run("injects default port for trojan protocol", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef: "node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "trojan", Port: 0},
				},
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.SupportedProtocols[0].Port != 10444 {
			t.Errorf("Expected trojan port=10444, got %d", node.Spec.SupportedProtocols[0].Port)
		}
	})

	t.Run("does not override explicitly set relayPort", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:       "node-1",
				Address:       "1.2.3.4",
				Region:        "us-west",
				Roles:         []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort:     9999,
				RelayProtocol: "socks5",
			},
		}
		if err := w.Default(ctx, node); err != nil {
			t.Fatalf("Default() error: %v", err)
		}
		if node.Spec.RelayPort != 9999 {
			t.Errorf("Expected relayPort=9999 (unchanged), got %d", node.Spec.RelayPort)
		}
	})

	t.Run("does not override explicitly set protocol port", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
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

func TestProxyNodeWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.ProxyNodeWebhook{}
	ctx := context.Background()

	t.Run("rejects empty address", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
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
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "invalid host",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for address with spaces, got nil")
		}
	})

	t.Run("rejects relayPort below 1024", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 80,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for relayPort=80, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "relayPort") {
			t.Errorf("Expected error to mention 'relayPort', got: %v", err)
		}
	})

	t.Run("rejects relayPort above 65535", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 70000,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for relayPort=70000, got nil")
		}
	})

	t.Run("rejects duplicate protocols", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
					{Protocol: "vless", Port: 10444},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for duplicate protocol, got nil")
		}
	})

	t.Run("rejects port conflict between relayPort and supportedProtocols", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10443,
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
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
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
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
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{},
				RelayPort: 10808,
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
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{"relay"},
				RelayPort: 10808,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err == nil {
			t.Error("Expected error for invalid role, got nil")
		}
	})

	t.Run("accepts valid ProxyNode with IP", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
				SupportedProtocols: []v1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for valid ProxyNode, got: %v", err)
		}
	})

	t.Run("accepts valid ProxyNode with hostname", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "example.com",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
				RelayPort: 10808,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for valid ProxyNode with hostname, got: %v", err)
		}
	})

	t.Run("accepts both inbound and outbound roles", func(t *testing.T) {
		node := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
				RelayPort: 10808,
			},
		}
		_, err := w.ValidateCreate(ctx, node)
		if err != nil {
			t.Errorf("Expected no error for inbound+outbound roles, got: %v", err)
		}
	})
}

func TestProxyNodeWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.ProxyNodeWebhook{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "1.2.3.4",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
			},
		}
		newNode := &v1alpha1.ProxyNode{
			Spec: v1alpha1.ProxyNodeSpec{
				NodeRef:   "node-1",
				Address:   "",
				Region:    "us-west",
				Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				RelayPort: 10808,
			},
		}
		_, err := w.ValidateUpdate(ctx, old, newNode)
		if err == nil {
			t.Error("Expected error for empty address on update, got nil")
		}
	})
}

func TestProxyUserWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.ProxyUserWebhook{}
	ctx := context.Background()

	t.Run("rejects empty protocol", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err == nil {
			t.Error("Expected error for empty protocol, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "protocol") {
			t.Errorf("Expected error to mention 'protocol', got: %v", err)
		}
	})

	t.Run("rejects unknown protocol", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
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
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
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

	t.Run("accepts valid ProxyUser with vless", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "vless",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for valid ProxyUser, got: %v", err)
		}
	})

	t.Run("accepts valid ProxyUser with trojan", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "trojan",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for trojan ProxyUser, got: %v", err)
		}
	})

	t.Run("accepts valid ProxyUser with socks5", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "socks5",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for socks5 ProxyUser, got: %v", err)
		}
	})

	t.Run("accepts valid ProxyUser with http", func(t *testing.T) {
		user := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "http",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		_, err := w.ValidateCreate(ctx, user)
		if err != nil {
			t.Errorf("Expected no error for http ProxyUser, got: %v", err)
		}
	})
}

func TestProxyUserWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.ProxyUserWebhook{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
				Protocol:   "vless",
				AuthSecret: corev1.SecretReference{Name: "my-secret"},
			},
		}
		newUser := &v1alpha1.ProxyUser{
			Spec: v1alpha1.ProxyUserSpec{
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

func TestProxyRouteWebhook_ValidateCreate(t *testing.T) {
	w := &webhook.ProxyRouteWebhook{}
	ctx := context.Background()

	t.Run("rejects empty inboundNode", func(t *testing.T) {
		route := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
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
		route := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
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
		route := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
				InboundNode:  "",
				OutboundNode: "",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err == nil {
			t.Error("Expected error for both empty nodes, got nil")
		}
	})

	t.Run("accepts valid ProxyRoute", func(t *testing.T) {
		route := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		_, err := w.ValidateCreate(ctx, route)
		if err != nil {
			t.Errorf("Expected no error for valid ProxyRoute, got: %v", err)
		}
	})
}

func TestProxyRouteWebhook_ValidateUpdate(t *testing.T) {
	w := &webhook.ProxyRouteWebhook{}
	ctx := context.Background()

	t.Run("validates new object on update", func(t *testing.T) {
		old := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
				InboundNode:  "inbound-1",
				OutboundNode: "outbound-1",
			},
		}
		newRoute := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
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

func TestProxyRouteWebhook_ValidateDelete(t *testing.T) {
	w := &webhook.ProxyRouteWebhook{}
	ctx := context.Background()

	t.Run("allows delete", func(t *testing.T) {
		route := &v1alpha1.ProxyRoute{
			Spec: v1alpha1.ProxyRouteSpec{
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
