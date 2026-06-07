package apiserver

import "encoding/json"

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
    "final": "local",
    "strategy": "ipv4_only",
    "independent_cache": true
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
      {"clash_mode": "Direct", "outbound": "direct"},
      {"clash_mode": "Proxy", "outbound": "proxy"},
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
        "download_detour": "direct"
      },
      {
        "tag": "category-ads-all",
        "type": "remote",
        "format": "binary",
        "url": "https://fastly.jsdelivr.net/gh/SagerNet/sing-geosite@rule-set/geosite-category-ads-all.srs",
        "download_detour": "direct"
      }
    ],
    "final": "proxy",
    "auto_detect_interface": true,
    "default_domain_resolver": "local"
  }
}`)

// MergeOutbounds replaces the "outbounds" array in templateJSON with generatedOutbounds.
// All other fields (inbounds, route, log) are preserved unchanged.
func MergeOutbounds(templateJSON []byte, generatedOutbounds []any) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(templateJSON, &m); err != nil {
		return nil, err
	}
	m["outbounds"] = generatedOutbounds
	return json.Marshal(m)
}
