package apiserver

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func sampleClientTemplate(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../config/samples/client-template-configmap.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := yaml.Unmarshal(data, &cm); err != nil {
		t.Fatal(err)
	}
	return []byte(cm.Data["config.json"])
}

func mergeDNSConfig(t *testing.T, template []byte, outbounds []any) map[string]any {
	t.Helper()
	result, err := MergeClientConfig(template, outbounds, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func dnsServerByTag(t *testing.T, dns map[string]any, tag string) map[string]any {
	t.Helper()
	for _, raw := range dns["servers"].([]any) {
		server := raw.(map[string]any)
		if server["tag"] == tag {
			return server
		}
	}
	t.Fatalf("DNS server %q not found", tag)
	return nil
}

func TestClientDNSExit(t *testing.T) {
	// An existing ConfigMap must be repaired too, including the old default
	// resolver and a stale detour referring to the removed proxy selector.
	legacy := []byte(`{"dns":{"servers":[{"type":"https","tag":"proxy-dns","server":"1.1.1.1","detour":"proxy"},{"type":"local","tag":"local"}],"final":"local","rules":[{"rule_set":"geosite-cn","server":"local"}]},"route":{"default_domain_resolver":"local","rules":[]}}`)
	for name, template := range map[string][]byte{
		"builtin":             DefaultTemplate,
		"sample":              sampleClientTemplate(t),
		"legacy":              legacy,
		"without-route-rules": []byte(`{"dns":{"servers":[{"type":"https","tag":"proxy-dns","server":"1.1.1.1"}]},"route":{}}`),
	} {
		for _, tc := range []struct {
			name   string
			groups []string
			want   string
		}{
			{"prefer-hk", []string{"us", "jp", "hk"}, "hk"},
			{"without-hk", []string{"us", "jp"}, "jp"},
			{"us-only", []string{"us"}, "us"},
			{"no-regional-group", nil, ""},
			{"ai-is-not-general-egress", []string{"ai"}, ""},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				outbounds := make([]any, 0, 2+len(tc.groups))
				outbounds = append(outbounds,
					map[string]any{"type": "socks", "tag": "node", "server": "192.0.2.1", "server_port": 1080},
					map[string]any{"type": "direct", "tag": "direct"},
				)
				for _, tag := range tc.groups {
					outbounds = append(outbounds, map[string]any{"type": "selector", "tag": tag, "outbounds": []string{"node"}})
				}
				config := mergeDNSConfig(t, template, outbounds)
				dns := config["dns"].(map[string]any)
				server := dnsServerByTag(t, dns, proxyDNSTag)
				if server["type"] != "https" || server["server"] != "1.1.1.1" {
					t.Fatalf("expected encrypted IP-addressed DNS, got %v", server)
				}
				detour, _ := server["detour"].(string)
				if detour != tc.want {
					t.Errorf("DNS detour = %q, want %q", detour, tc.want)
				}
				if dns["final"] != proxyDNSTag {
					t.Errorf("dns.final = %v, want proxy-dns", dns["final"])
				}
				route := config["route"].(map[string]any)
				if name == "sample" && tc.want == "" && route["final"] != "direct" {
					t.Fatalf("sample without regional groups has dangling route.final: %v", route["final"])
				}
				if route["default_domain_resolver"] != proxyDNSTag {
					t.Errorf("default_domain_resolver = %v, want proxy-dns", route["default_domain_resolver"])
				}
				// Preserve only the template's explicit local-domain exceptions;
				// never add local fallback or mode-wide local DNS rules.
				var original map[string]any
				if err := json.Unmarshal(template, &original); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(dns["rules"], original["dns"].(map[string]any)["rules"]) {
					t.Fatalf("DNS rules changed: %v", dns["rules"])
				}
			})
		}
	}
}

func TestClientDNSBootstrapIsLimitedToProxyHostnames(t *testing.T) {
	for _, template := range [][]byte{
		DefaultTemplate,
		[]byte(`{"dns":{"servers":[{"type":"https","tag":"proxy-dns","server":"1.1.1.1"}]},"route":{"rules":[]}}`),
	} {
		outbounds := []any{
			map[string]any{"type": "socks", "tag": "hostname", "server": "entry.example.com", "server_port": 1080},
			map[string]any{"type": "socks", "tag": "ipv4", "server": "192.0.2.1", "server_port": 1080},
			map[string]any{"type": "socks", "tag": "ipv6", "server": "2001:db8::1", "server_port": 1080},
			map[string]any{"type": "socks", "tag": "explicit", "server": "entry.example.com", "server_port": 1080, "domain_resolver": map[string]any{"server": localDNSTag, "strategy": "ipv4_only"}},
			map[string]any{"type": "selector", "tag": "hk", "outbounds": []string{"hostname"}},
			map[string]any{"type": "direct", "tag": "direct"},
		}
		before, err := json.Marshal(outbounds)
		if err != nil {
			t.Fatal(err)
		}
		config := mergeDNSConfig(t, template, outbounds)
		for _, raw := range config["outbounds"].([]any) {
			outbound := raw.(map[string]any)
			switch outbound["tag"] {
			case "hostname":
				if outbound["domain_resolver"] != localDNSTag {
					t.Fatalf("proxy hostname must bootstrap locally: %v", outbound)
				}
			case "explicit":
				if !reflect.DeepEqual(outbound["domain_resolver"], outbounds[3].(map[string]any)["domain_resolver"]) {
					t.Fatalf("explicit resolver changed: %v", outbound)
				}
			default:
				if _, ok := outbound["domain_resolver"]; ok {
					t.Fatalf("unexpected local resolver on %v", outbound)
				}
			}
		}
		dns := config["dns"].(map[string]any)
		if dnsServerByTag(t, dns, localDNSTag)["type"] != "local" {
			t.Fatal("missing bootstrap resolver")
		}
		after, err := json.Marshal(outbounds)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("MergeClientConfig mutated caller's outbounds")
		}
	}
}

func TestClientDNSUnmanagedTemplateIsPreserved(t *testing.T) {
	template := []byte(`{"dns":{"servers":[{"type":"local","tag":"custom"}],"final":"custom","rules":[{"domain":"example.com","server":"custom"}]},"route":{"default_domain_resolver":"custom","rules":[]}}`)
	outbounds := []any{map[string]any{"type": "socks", "tag": "node", "server": "entry.example.com", "server_port": 1080}}
	config := mergeDNSConfig(t, template, outbounds)
	var original map[string]any
	if err := json.Unmarshal(template, &original); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config["dns"], original["dns"]) {
		t.Fatal("unmanaged DNS changed")
	}
	if config["route"].(map[string]any)["default_domain_resolver"] != "custom" {
		t.Fatal("unmanaged default resolver changed")
	}
	if _, ok := config["outbounds"].([]any)[0].(map[string]any)["domain_resolver"]; ok {
		t.Fatal("unmanaged outbound resolver changed")
	}
}

func TestClientDNSSampleDoesNotSendGlobalClientSubnet(t *testing.T) {
	config := mergeDNSConfig(t, sampleClientTemplate(t), nil)
	if _, ok := config["dns"].(map[string]any)["client_subnet"]; ok {
		t.Fatal("sample must not attach a domestic client subnet to overseas DNS queries")
	}
}
