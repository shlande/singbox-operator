package apiserver

import (
	"encoding/json"
	"testing"
)

func TestMergeClientConfig_NormalizesDeprecatedTemplateFields(t *testing.T) {
	template := []byte(`{"dns":{"independent_cache":true},"route":{"rules":[],"rule_set":[{"type":"remote","tag":"set","download_detour":"direct"}]}}`)
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
	set := config["route"].(map[string]any)["rule_set"].([]any)[0].(map[string]any)
	if _, ok := set["download_detour"]; ok {
		t.Fatal("download_detour should be removed")
	}
	client, ok := set["http_client"].(map[string]any)
	if !ok || client["detour"] != "direct" {
		t.Fatalf("http_client = %#v, want direct detour", set["http_client"])
	}
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
