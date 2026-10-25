package apiserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestClientDNSResolveUsesRoutingBeforeBusinessRules(t *testing.T) {
	config := mergeDNSConfig(t, DefaultTemplate, []any{
		map[string]any{"type": "selector", "tag": "jp", "outbounds": []string{"node"}},
		map[string]any{"type": "direct", "tag": "direct"},
	})
	rules := config["route"].(map[string]any)["rules"].([]any)
	if rules[0].(map[string]any)["action"] != "sniff" || rules[1].(map[string]any)["action"] != "hijack-dns" {
		t.Fatal("DNS interception must precede destination resolution")
	}
	if !reflect.DeepEqual(rules[2], map[string]any{"action": "resolve"}) {
		t.Fatalf("expected resolve without server override before business rules, got %v", rules[2])
	}
	// Custom templates without managed DNS must not gain eager resolution.
	custom := mergeDNSConfig(t, []byte(`{"dns":{"servers":[{"type":"local","tag":"custom"}]},"route":{"rules":[]}}`), nil)
	customRules, _ := custom["route"].(map[string]any)["rules"].([]any)
	for _, raw := range customRules {
		if raw.(map[string]any)["action"] == "resolve" {
			t.Fatal("unmanaged template gained resolve preprocessing")
		}
	}
}

func TestClientDNSRegionalServersPreserveTransportOptions(t *testing.T) {
	template := []byte(`{"dns":{"servers":[{"type":"https","tag":"proxy-dns","server":"1.1.1.1","server_port":8443,"path":"/custom-dns","tls":{"server_name":"cloudflare-dns.com"},"headers":{"X-Test":"value"}}],"rules":[{"domain_suffix":"internal.test","server":"proxy-dns"}]},"route":{"rules":[]}}`)
	config := mergeDNSConfig(t, template, []any{map[string]any{"type": "selector", "tag": "jp", "outbounds": []string{"node"}}})
	dns := config["dns"].(map[string]any)
	base := dnsServerByTag(t, dns, proxyDNSTag)
	regional := dnsServerByTag(t, dns, proxyDNSTag+"-jp")
	for _, key := range []string{"type", "server", "server_port", "path", "tls", "headers"} {
		if !reflect.DeepEqual(regional[key], base[key]) {
			t.Errorf("regional server lost %s: %v", key, regional)
		}
	}
}

func TestClientDNSRejectsReservedRegionalTagCollision(t *testing.T) {
	for _, mode := range clientRegionModes {
		t.Run(mode.name, func(t *testing.T) {
			var template map[string]any
			if err := json.Unmarshal(DefaultTemplate, &template); err != nil {
				t.Fatal(err)
			}
			dns := template["dns"].(map[string]any)
			tag := proxyDNSTag + "-" + mode.tag
			dns["servers"] = append(dns["servers"].([]any), map[string]any{"type": "local", "tag": tag})
			data, err := json.Marshal(template)
			if err != nil {
				t.Fatal(err)
			}
			_, err = MergeClientConfig(data, []any{map[string]any{"type": "selector", "tag": mode.tag}}, ClientConfigInput{})
			if err == nil || !strings.Contains(err.Error(), tag) {
				t.Fatalf("expected reserved tag collision, got %v", err)
			}
		})
	}
}
