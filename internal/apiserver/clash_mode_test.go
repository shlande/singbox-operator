package apiserver

import (
	"encoding/json"
	"testing"
)

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
