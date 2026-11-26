package apiserver

import (
	"encoding/json"
	"testing"
)

func TestMergeClientConfig_NormalizesDeprecatedTemplateFields(t *testing.T) {
	template := []byte(`{"dns":{"independent_cache":true},"inbounds":[{"type":"tun","tag":"tun-in","stack":"gvisor"}],"route":{"rules":[],"rule_set":[{"type":"remote","tag":"set","download_detour":"direct"},{"type":"remote","tag":"inline-direct","http_client":{"detour":"direct"}},{"type":"remote","tag":"proxy-set","download_detour":"hk"}]}}`)
	result, err := MergeClientConfig(template, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["dns"].(map[string]any)["independent_cache"]; ok {
		t.Fatal("independent_cache should be removed")
	}
	tunInbound := config["inbounds"].([]any)[0].(map[string]any)
	if _, ok := tunInbound["stack"]; ok {
		t.Fatalf("tun stack should be removed for sing-box 1.15+, got %#v", tunInbound)
	}
	clients, ok := config["http_clients"].([]any)
	if !ok || len(clients) != 1 {
		t.Fatalf("http_clients = %#v, want single direct-http client", config["http_clients"])
	}
	directClient, ok := clients[0].(map[string]any)
	if !ok || directClient["tag"] != defaultDirectHTTPClient {
		t.Fatalf("http_clients[0] = %#v, want tag %q", clients[0], defaultDirectHTTPClient)
	}
	if _, hasDetour := directClient["detour"]; hasDetour {
		t.Fatalf("http_clients[0] must not set empty direct detour: %#v", directClient)
	}

	sets := config["route"].(map[string]any)["rule_set"].([]any)
	for _, idx := range []int{0, 1} {
		set := sets[idx].(map[string]any)
		if _, ok := set["download_detour"]; ok {
			t.Fatalf("rule_set[%d]: download_detour should be removed", idx)
		}
		if set["http_client"] != defaultDirectHTTPClient {
			t.Fatalf("rule_set[%d].http_client = %#v, want %q", idx, set["http_client"], defaultDirectHTTPClient)
		}
	}
	proxySet := sets[2].(map[string]any)
	if _, ok := proxySet["download_detour"]; ok {
		t.Fatal("download_detour should be removed on non-direct set")
	}
	proxyClient, ok := proxySet["http_client"].(map[string]any)
	if !ok || proxyClient["detour"] != "hk" {
		t.Fatalf("proxySet.http_client = %#v, want hk detour", proxySet["http_client"])
	}
}

func TestMergeClientConfig_NormalizesDirectHTTPClientEdgeCases(t *testing.T) {
	t.Run("strips-direct-detour-from-existing-http-clients-and-avoids-tag-collision", func(t *testing.T) {
		template := []byte(`{"http_clients":[{"tag":"direct-http","detour":"hk"},{"tag":"custom","detour":"direct"}],"route":{"rules":[],"rule_set":[{"type":"remote","tag":"set","http_client":{"detour":"direct"}},{"type":"remote","tag":"opts","http_client":{"detour":"direct","version":1}}]}}`)
		result, err := MergeClientConfig(template, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(result, &config); err != nil {
			t.Fatal(err)
		}
		clients := config["http_clients"].([]any)
		if len(clients) != 2 {
			t.Fatalf("expected existing clients without adding extra entry, got %#v", clients)
		}
		if _, hasDetour := clients[1].(map[string]any)["detour"]; hasDetour {
			t.Fatalf("existing direct http_client still has detour: %#v", clients[1])
		}
		sets := config["route"].(map[string]any)["rule_set"].([]any)
		if sets[0].(map[string]any)["http_client"] != "custom" {
			t.Fatalf("expected reuse of custom direct client tag, got %#v", sets[0].(map[string]any)["http_client"])
		}
		optsClient := sets[1].(map[string]any)["http_client"].(map[string]any)
		if _, hasDetour := optsClient["detour"]; hasDetour || optsClient["version"] != float64(1) {
			t.Fatalf("expected direct detour removed while preserving version: %#v", optsClient)
		}
	})

	t.Run("creates-unique-tag-when-default-tag-is-in-use", func(t *testing.T) {
		template := []byte(`{"http_clients":[{"tag":"direct-http","detour":"hk"}],"route":{"rules":[],"rule_set":[{"type":"remote","tag":"set","http_client":{"detour":"direct"}}]}}`)
		result, err := MergeClientConfig(template, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(result, &config); err != nil {
			t.Fatal(err)
		}
		clients := config["http_clients"].([]any)
		if len(clients) != 2 {
			t.Fatalf("expected new non-colliding client, got %#v", clients)
		}
		newClient := clients[1].(map[string]any)
		if newClient["tag"] != "direct-http-1" {
			t.Fatalf("newClient.tag = %#v, want direct-http-1", newClient["tag"])
		}
		set := config["route"].(map[string]any)["rule_set"].([]any)[0].(map[string]any)
		if set["http_client"] != "direct-http-1" {
			t.Fatalf("rule_set.http_client = %#v, want direct-http-1", set["http_client"])
		}
	})

	t.Run("default-and-sample-templates-never-detour-to-direct", func(t *testing.T) {
		for name, tmpl := range map[string][]byte{
			"default": DefaultTemplate,
			"sample":  sampleClientTemplate(t),
		} {
			result, err := MergeClientConfig(tmpl, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var config map[string]any
			if err := json.Unmarshal(result, &config); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			defined := map[string]bool{}
			for _, raw := range config["http_clients"].([]any) {
				c := raw.(map[string]any)
				if c["detour"] == "direct" {
					t.Fatalf("%s: http_clients entry has direct detour: %#v", name, c)
				}
				defined[c["tag"].(string)] = true
			}
			for _, raw := range config["route"].(map[string]any)["rule_set"].([]any) {
				set := raw.(map[string]any)
				if set["type"] != "remote" {
					continue
				}
				tag, ok := set["http_client"].(string)
				if !ok || !defined[tag] {
					t.Fatalf("%s: remote rule_set %v http_client = %#v, want defined tag in %v", name, set["tag"], set["http_client"], defined)
				}
			}
			for _, raw := range config["inbounds"].([]any) {
				inbound := raw.(map[string]any)
				if inbound["type"] == "tun" {
					if _, hasStack := inbound["stack"]; hasStack {
						t.Fatalf("%s: tun inbound must not specify deprecated stack: %#v", name, inbound)
					}
				}
			}
		}
	})
}

func TestMergeClientConfig_NoHKUsesJPForFinalAndAuto(t *testing.T) {
	generated := []any{
		map[string]any{"type": "selector", "tag": "jp", "outbounds": []string{"jp-node"}},
		map[string]any{"type": "selector", "tag": "us", "outbounds": []string{"us-node"}},
		map[string]any{"type": "direct", "tag": "direct"},
	}
	result, err := MergeClientConfig(DefaultTemplate, generated, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	route := config["route"].(map[string]any)
	if route["final"] != "jp" {
		t.Fatalf("route.final = %v, want jp", route["final"])
	}
	foundAuto := false
	for _, raw := range route["rules"].([]any) {
		rule := raw.(map[string]any)
		if rule["clash_mode"] == "Auto" {
			foundAuto = true
			if rule["outbound"] != "jp" {
				t.Fatalf("Auto outbound = %v, want jp", rule["outbound"])
			}
		}
	}
	if !foundAuto {
		t.Fatal("missing Auto clash mode rule")
	}
}

func TestMergeClientConfig_DefaultDNSUsesProxyDNS(t *testing.T) {
	result, err := MergeClientConfig(DefaultTemplate, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	if got := config["dns"].(map[string]any)["final"]; got != "proxy-dns" {
		t.Fatalf("dns.final = %v, want proxy-dns", got)
	}
}

func TestMergeClientConfig_CustomDNSWithoutProxyDNSPreservesFinal(t *testing.T) {
	template := []byte(`{"dns":{"servers":[{"type":"local","tag":"local"}],"final":"local"},"route":{"rules":[]}}`)
	result, err := MergeClientConfig(template, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	if got := config["dns"].(map[string]any)["final"]; got != "local" {
		t.Fatalf("dns.final = %v, want local", got)
	}
}

func TestMergeClientConfig_DefaultsRuleModeToHK(t *testing.T) {
	generated := []any{
		map[string]any{"type": "selector", "tag": "hk", "outbounds": []string{"hk-node"}},
		map[string]any{"type": "selector", "tag": "jp", "outbounds": []string{"jp-node"}},
		map[string]any{"type": "selector", "tag": "us", "outbounds": []string{"us-node"}},
		map[string]any{"type": "selector", "tag": "ai", "outbounds": []string{"ai-node"}},
		map[string]any{"type": "direct", "tag": "direct"},
	}
	result, err := MergeClientConfig(DefaultTemplate, generated, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatal(err)
	}
	selectors := map[string]bool{}
	for _, raw := range config["outbounds"].([]any) {
		ob := raw.(map[string]any)
		if ob["type"] == "selector" {
			selectors[ob["tag"].(string)] = true
		}
	}
	if len(selectors) != 4 || !selectors["hk"] || !selectors["jp"] || !selectors["us"] || !selectors["ai"] {
		t.Fatalf("selectors = %v, want exactly hk/jp/us/ai", selectors)
	}
	experimental := config["experimental"].(map[string]any)
	if got := experimental["clash_api"].(map[string]any)["default_mode"]; got != "Auto" {
		t.Fatalf("default_mode = %v, want Auto", got)
	}
	route := config["route"].(map[string]any)
	if route["final"] != "hk" {
		t.Fatalf("route.final = %v, want hk", route["final"])
	}
	seen := map[string]bool{}
	for _, raw := range route["rules"].([]any) {
		rule := raw.(map[string]any)
		if mode, ok := rule["clash_mode"].(string); ok {
			seen[mode] = true
		}
	}
	for _, mode := range []string{"Auto", "HK", "JP", "US"} {
		if !seen[mode] {
			t.Errorf("missing clash mode %s", mode)
		}
	}
	if seen["AI"] {
		t.Error("AI must not be a Clash mode")
	}
}
