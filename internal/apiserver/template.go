package apiserver

import "encoding/json"

// DefaultTemplate is the built-in client config template used when no admin template is provided.
// It provides local socks5/http inbounds and basic CN split routing.
var DefaultTemplate = []byte(`{
  "log": {"level": "info"},
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
      {"type": "logical", "mode": "or", "rules": [{"geoip": "cn"}, {"geosite": "cn"}], "outbound": "direct"}
    ],
    "final": "proxy",
    "auto_detect_interface": true
  }
}`)

// MergeOutbounds replaces the "outbounds" array in templateJSON with generatedOutbounds.
// All other fields (inbounds, route, log) are preserved unchanged.
func MergeOutbounds(templateJSON []byte, generatedOutbounds []interface{}) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(templateJSON, &m); err != nil {
		return nil, err
	}
	m["outbounds"] = generatedOutbounds
	return json.Marshal(m)
}
