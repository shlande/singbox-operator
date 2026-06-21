package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

func TestHostPortProtocols_TUIC(t *testing.T) {
	protocols := hostPortProtocols("tuic")
	if len(protocols) != 1 {
		t.Errorf("expected 1 protocol for tuic, got %d: %v", len(protocols), protocols)
	}
	if protocols[0] != corev1.ProtocolUDP {
		t.Errorf("expected UDP for tuic, got %v", protocols[0])
	}
	// Also verify hysteria2 still returns UDP only
	h2 := hostPortProtocols("hysteria2")
	if len(h2) != 1 || h2[0] != corev1.ProtocolUDP {
		t.Errorf("hysteria2 should still return [UDP], got %v", h2)
	}
	// naive and anytls should return TCP (default)
	for _, proto := range []string{"naive", "anytls", "vless", "trojan"} {
		p := hostPortProtocols(proto)
		if len(p) != 1 || p[0] != corev1.ProtocolTCP {
			t.Errorf("protocol %s should return [TCP], got %v", proto, p)
		}
	}
}

func TestNeedsTLS_NewProtocols(t *testing.T) {
	makeNodeWithProto := func(proto string) *proxyv1alpha1.SingBoxNode {
		return &proxyv1alpha1.SingBoxNode{
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: proto, Port: 10443},
				},
			},
		}
	}
	// TLS-requiring protocols
	for _, proto := range []string{"hysteria2", "tuic", "naive", "anytls"} {
		if !needsTLS(makeNodeWithProto(proto)) {
			t.Errorf("needsTLS should return true for protocol %s", proto)
		}
	}
	// Non-TLS protocols
	for _, proto := range []string{"vless", "trojan", "socks5", "http"} {
		if needsTLS(makeNodeWithProto(proto)) {
			t.Errorf("needsTLS should return false for protocol %s", proto)
		}
	}
}
