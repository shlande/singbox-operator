package apiserver

import (
	"encoding/json"
	"fmt"
)

// DefaultTemplate is the built-in client config template used when no admin template is provided.
// It provides local socks5/http inbounds and basic CN split routing.
var DefaultTemplate = []byte(`{
  "log": {"level": "info"},
  "dns": {
    "servers": [
      {"type": "https", "tag": "proxy-dns", "server": "1.1.1.1"},
      {"type": "hosts", "path": [], "predefined": {}, "tag": "block"},
      {"type": "local", "tag": "local"}
    ],
    "rules": [
      {"rule_set": "geosite-cn", "server": "local"}
    ],
    "final": "proxy-dns",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "address": "172.19.0.1/30",
      "auto_route": true,
      "strict_route": true,
      "stack": "gvisor",
      "platform": {
        "http_proxy": {
          "enabled": true,
          "server": "127.0.0.1",
          "server_port": 7891
        }
      }
    },
    {"type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": 7890},
    {"type": "http", "tag": "http-in", "listen": "127.0.0.1", "listen_port": 7891}
  ],
  "outbounds": [],
  "route": {
    "rules": [
      {"action": "sniff"},
      {"protocol": "dns", "action": "hijack-dns"},
      {"rule_set": ["geosite-cn"], "outbound": "direct"},
      {"ip_is_private": true, "outbound": "direct"},
      {"rule_set": ["category-ads-all"], "action": "reject"}
    ],
    "rule_set": [
      {
        "tag": "geosite-cn",
        "type": "remote",
        "format": "binary",
        "url": "https://fastly.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-cn.srs",
        "http_client": {"detour": "direct"}
      },
      {
        "tag": "category-ads-all",
        "type": "remote",
        "format": "binary",
        "url": "https://fastly.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-category-ads-all.srs",
        "http_client": {"detour": "direct"}
      }
    ],
    "final": "direct",
    "auto_detect_interface": true,
    "default_domain_resolver": "local"
  },
  "experimental": {"clash_api": {"default_mode": "Auto"}}
}`)

// MergeOutbounds replaces the "outbounds" array in templateJSON with generatedOutbounds.
// All other fields (inbounds, route, log) are preserved unchanged.
func MergeOutbounds(templateJSON []byte, generatedOutbounds []any) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(templateJSON, &m); err != nil {
		return nil, err
	}
	normalizeDeprecatedClientConfig(&m)
	m["outbounds"] = generatedOutbounds
	return json.Marshal(m)
}

// normalizeDeprecatedClientConfig migrates template fields removed in sing-box
// 1.16. Inline HTTP clients preserve each rule-set's old download detour
// without requiring a new top-level client registry.
func normalizeDeprecatedClientConfig(config *map[string]any) {
	m := *config
	if dns, ok := m["dns"].(map[string]any); ok {
		delete(dns, "independent_cache")
	}
	route, ok := m["route"].(map[string]any)
	if !ok {
		return
	}
	sets, ok := route["rule_set"].([]any)
	if !ok {
		return
	}
	for _, raw := range sets {
		set, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		detour, ok := set["download_detour"].(string)
		if !ok {
			continue
		}
		if _, hasHTTPClient := set["http_client"]; !hasHTTPClient {
			set["http_client"] = map[string]any{"detour": detour}
		}
		delete(set, "download_detour")
	}
}

func isDomesticDirectRule(rule map[string]any) bool {
	if rule["outbound"] != "direct" {
		return false
	}
	if rule["ip_is_private"] == true {
		return true
	}
	sets, ok := rule["rule_set"].([]any)
	if !ok {
		return false
	}
	for _, set := range sets {
		if set == "geosite-cn" {
			return true
		}
	}
	return false
}

// MergeClientConfig merges generated outbounds and client policy/mode rules.
// The template's existing non-generated rules retain their relative order.
func MergeClientConfig(templateJSON []byte, generatedOutbounds []any, input ClientConfigInput) ([]byte, error) {
	_, policyRules, _, err := buildClientPolicies(input)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(templateJSON, &m); err != nil {
		return nil, err
	}
	normalizeDeprecatedClientConfig(&m)
	route, ok := m["route"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("template route must be an object")
	}
	original, ok := route["rules"].([]any)
	if !ok {
		if len(input.EgressPolicies) == 0 {
			return MergeOutbounds(templateJSON, generatedOutbounds)
		}
		return nil, fmt.Errorf("template route.rules must be an array")
	}
	defined := make(map[string]bool)
	if sets, ok := route["rule_set"].([]any); ok {
		for _, set := range sets {
			if entry, ok := set.(map[string]any); ok {
				if tag, ok := entry["tag"].(string); ok {
					defined[tag] = true
				}
			}
		}
	}
	for _, p := range input.EgressPolicies {
		for _, ref := range p.Spec.Match.RuleSet {
			if !defined[ref] {
				return nil, fmt.Errorf("policy %s: rule_set %q is not defined in template route.rule_set", p.Name, ref)
			}
		}
	}

	has := make(map[string]bool)
	for _, ob := range generatedOutbounds {
		if v, ok := ob.(map[string]any); ok {
			if tag, ok := v["tag"].(string); ok {
				has[tag] = true
			}
		}
	}
	insert := 0
	for insert < len(original) {
		rule, ok := original[insert].(map[string]any)
		if !ok || (rule["action"] != "sniff" && rule["action"] != "hijack-dns") {
			break
		}
		insert++
	}
	rules := append([]any(nil), original[:insert]...)
	// Explicit AI policy traffic must precede domestic and user mode rules.
	rules = append(rules, policyRules...)
	for _, item := range original[insert:] {
		if rule, ok := item.(map[string]any); ok && isDomesticDirectRule(rule) {
			rules = append(rules, rule)
		}
	}
	proxySelector := ""
	for _, tag := range []string{"hk", "jp", "us"} {
		if has[tag] {
			proxySelector = tag
			break
		}
	}
	modeTags := []struct {
		name string
		tag  string
	}{{"HK", "hk"}, {"JP", "jp"}, {"US", "us"}}
	if proxySelector != "" {
		rules = append(rules, map[string]any{"clash_mode": "Auto", "outbound": proxySelector})
	}
	for _, mode := range modeTags {
		if has[mode.tag] {
			rules = append(rules, map[string]any{"clash_mode": mode.name, "outbound": mode.tag})
		}
	}
	for _, item := range original[insert:] {
		rule, ok := item.(map[string]any)
		if ok && isDomesticDirectRule(rule) {
			continue
		}
		if ok && (rule["clash_mode"] == "Proxy" || rule["clash_mode"] == "proxy") {
			continue
		}
		if ok && rule["outbound"] == "proxy" {
			// Old templates may contain a proxy reference. Never leave a dangling tag.
			copy := make(map[string]any, len(rule))
			for k, v := range rule {
				if k != "outbound" {
					copy[k] = v
				}
			}
			copy["outbound"] = "direct"
			item = copy
		}
		rules = append(rules, item)
	}
	route["rules"] = rules
	experimental, _ := m["experimental"].(map[string]any)
	if experimental == nil {
		experimental = make(map[string]any)
		m["experimental"] = experimental
	}
	clash, _ := experimental["clash_api"].(map[string]any)
	if clash == nil {
		clash = make(map[string]any)
		experimental["clash_api"] = clash
	}
	// Auto and route.final must use the same available regional selector. HK is
	// preferred, but clusters without HK should still route through JP or US.
	clash["default_mode"] = "Auto"
	if proxySelector != "" {
		route["final"] = proxySelector
	}

	if dns, ok := m["dns"].(map[string]any); ok {
		if servers, ok := dns["servers"].([]any); ok {
			for _, server := range servers {
				entry, ok := server.(map[string]any)
				if ok && entry["tag"] == "proxy-dns" {
					dns["final"] = "proxy-dns"
					break
				}
			}
		}
	}
	m["outbounds"] = generatedOutbounds
	return json.Marshal(m)
}
