package configengine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// UserCredential holds authentication credentials for a ProxyUser.
type UserCredential struct {
	UUID     string
	Password string
	Username string
}

// NodeCredential holds the SOCKS5 credentials for inter-node relay.
type NodeCredential struct {
	Username string
	Password string
}

// Input contains all data needed to compute a node's sing-box config.
type Input struct {
	Node                *v1alpha1.ProxyNode
	Users               []*v1alpha1.ProxyUser
	UserCreds           map[string]UserCredential
	OutboundNodes       []*v1alpha1.ProxyNode
	Routes              []*v1alpha1.ProxyRoute
	NodeCreds           map[string]NodeCredential
	OutboundNodesByName map[string]*v1alpha1.ProxyNode
}

// Output contains the computed sing-box config.
type Output struct {
	Config []byte
	Hash   string
}

// singboxConfig mirrors the top-level sing-box config.json structure.
type singboxConfig struct {
	Log       logConfig     `json:"log"`
	Inbounds  []interface{} `json:"inbounds"`
	Outbounds []interface{} `json:"outbounds"`
	Route     routeConfig   `json:"route"`
}

type logConfig struct {
	Level string `json:"level"`
}

type routeRule struct {
	Inbound  []string `json:"inbound"`
	Outbound string   `json:"outbound"`
}

type routeConfig struct {
	Rules []routeRule `json:"rules,omitempty"`
	Final string      `json:"final"`
}

// Compute calculates the complete sing-box config.json for a given ProxyNode.
func Compute(input Input) (Output, error) {
	node := input.Node

	isInbound := hasRole(node, v1alpha1.ProxyRoleInbound)
	isOutbound := hasRole(node, v1alpha1.ProxyRoleOutbound)

	// Sort routes by name for deterministic port assignment and hash stability.
	myRoutes := routesForNode(input)

	var inbounds []interface{}
	var outbounds []interface{}
	var rules []routeRule

	if isInbound {
		if len(myRoutes) > 0 {
			ibs, rls := buildRouteUserInbounds(input, myRoutes)
			inbounds = append(inbounds, ibs...)
			rules = rls
		} else {
			inbounds = append(inbounds, buildUserInbounds(input)...)
		}
		outbounds = append(outbounds, buildOutboundNodeOutbounds(input, myRoutes)...)
		outbounds = append(outbounds, buildRouteOutbounds(input, myRoutes)...)
	}

	if isOutbound {
		inbounds = append(inbounds, buildRelayInbound(input))
	}

	// Always append direct; deduplicate by tag.
	outbounds = deduplicateByTag(append(outbounds, buildDirectOutbound()))

	if inbounds == nil {
		inbounds = []interface{}{}
	}

	final := routeFinal(outbounds)

	cfg := singboxConfig{
		Log:       logConfig{Level: "info"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route: routeConfig{
			Rules: rules,
			Final: final,
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return Output{}, fmt.Errorf("marshal config: %w", err)
	}

	return Output{
		Config: data,
		Hash:   ComputeHash(data),
	}, nil
}

// ComputeHash returns the first 16 hex chars of sha256(config).
func ComputeHash(config []byte) string {
	h := sha256.Sum256(config)
	return fmt.Sprintf("%x", h[:8])
}

// ExtractNodePorts returns all ports that need NodePort Services.
func ExtractNodePorts(node *v1alpha1.ProxyNode) []int32 {
	var ports []int32
	for _, p := range node.Spec.SupportedProtocols {
		ports = append(ports, p.Port)
	}
	if hasRole(node, v1alpha1.ProxyRoleOutbound) && node.Spec.RelayPort > 0 {
		ports = append(ports, node.Spec.RelayPort)
	}
	return ports
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func hasRole(node *v1alpha1.ProxyNode, role v1alpha1.ProxyRole) bool {
	for _, r := range node.Spec.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func findProtocolPort(node *v1alpha1.ProxyNode, protocol string) int32 {
	for _, p := range node.Spec.SupportedProtocols {
		if p.Protocol == protocol {
			return p.Port
		}
	}
	return 0
}

// routesForNode returns routes targeting this inbound node, sorted by name
// for deterministic port assignment across reconcile cycles.
func routesForNode(input Input) []*v1alpha1.ProxyRoute {
	var result []*v1alpha1.ProxyRoute
	for _, r := range input.Routes {
		if r.Spec.InboundNode == input.Node.Name {
			result = append(result, r)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// routePortOffset computes a port offset for a given route index and protocol
// index such that no two (routeIdx, protoIdx) pairs produce the same offset.
// Layout: offset = routeIdx * numProtocols + protoIdx
// Example: 2 routes, protocols=[vless:30080, trojan:30081], numProtocols=2
//
//	vless route[0] = 30080 + 0*2 + 0 = 30080
//	trojan route[0] = 30081 + 0*2 + 0 = 30081  (base already differs)
//	vless route[1] = 30080 + 1*2 + 0 = 30082
//	trojan route[1] = 30081 + 1*2 + 0 = 30083
func routePort(basePort int32, routeIdx, protoIdx, numProtocols int) int32 {
	return basePort + int32(routeIdx*numProtocols+protoIdx)
}

// buildRouteUserInbounds creates one inbound listener per user per route,
// each on a distinct port derived from the protocol base port + route offset.
// It also returns the routing rules that bind each inbound tag to its outbound.
func buildRouteUserInbounds(input Input, routes []*v1alpha1.ProxyRoute) ([]interface{}, []routeRule) {
	numProtocols := len(input.Node.Spec.SupportedProtocols)
	if numProtocols == 0 {
		numProtocols = 1
	}

	var inbounds []interface{}
	var rules []routeRule

	for routeIdx, route := range routes {
		outboundTag := fmt.Sprintf("outbound-%s", route.Spec.OutboundNode)
		var inboundTagsForRoute []string

		for protoIdx, proto := range input.Node.Spec.SupportedProtocols {
			port := routePort(proto.Port, routeIdx, protoIdx, numProtocols)

			for _, user := range input.Users {
				if user.Spec.Protocol != proto.Protocol {
					continue
				}
				cred := input.UserCreds[user.Name]
				tag := fmt.Sprintf("inbound-%s-%s-%s", proto.Protocol, user.Name, route.Spec.OutboundNode)
				inboundTagsForRoute = append(inboundTagsForRoute, tag)

				switch proto.Protocol {
				case "vless":
					inbounds = append(inbounds, map[string]interface{}{
						"type":        "vless",
						"tag":         tag,
						"listen":      "::",
						"listen_port": port,
						"users":       []map[string]interface{}{{"uuid": cred.UUID}},
					})
				case "trojan":
					inbounds = append(inbounds, map[string]interface{}{
						"type":        "trojan",
						"tag":         tag,
						"listen":      "::",
						"listen_port": port,
						"users":       []map[string]interface{}{{"password": cred.Password}},
					})
				case "socks5":
					inbounds = append(inbounds, map[string]interface{}{
						"type":        "socks",
						"tag":         tag,
						"listen":      "::",
						"listen_port": port,
						"users":       []map[string]interface{}{{"username": cred.Username, "password": cred.Password}},
					})
				case "http":
					inbounds = append(inbounds, map[string]interface{}{
						"type":        "http",
						"tag":         tag,
						"listen":      "::",
						"listen_port": port,
						"users":       []map[string]interface{}{{"username": cred.UUID, "password": cred.Password}},
					})
				}
			}
		}

		if len(inboundTagsForRoute) > 0 {
			rules = append(rules, routeRule{
				Inbound:  inboundTagsForRoute,
				Outbound: outboundTag,
			})
		}
	}

	return inbounds, rules
}

// buildUserInbounds creates one inbound per user (fallback when no Routes exist).
func buildUserInbounds(input Input) []interface{} {
	var result []interface{}
	for _, user := range input.Users {
		cred := input.UserCreds[user.Name]
		port := findProtocolPort(input.Node, user.Spec.Protocol)
		tag := fmt.Sprintf("inbound-%s-%s", user.Spec.Protocol, user.Name)

		switch user.Spec.Protocol {
		case "vless":
			result = append(result, map[string]interface{}{
				"type":        "vless",
				"tag":         tag,
				"listen":      "::",
				"listen_port": port,
				"users":       []map[string]interface{}{{"uuid": cred.UUID}},
			})
		case "trojan":
			result = append(result, map[string]interface{}{
				"type":        "trojan",
				"tag":         tag,
				"listen":      "::",
				"listen_port": port,
				"users":       []map[string]interface{}{{"password": cred.Password}},
			})
		case "socks5":
			result = append(result, map[string]interface{}{
				"type":        "socks",
				"tag":         tag,
				"listen":      "::",
				"listen_port": port,
				"users":       []map[string]interface{}{{"username": cred.Username, "password": cred.Password}},
			})
		case "http":
			result = append(result, map[string]interface{}{
				"type":        "http",
				"tag":         tag,
				"listen":      "::",
				"listen_port": port,
				"users":       []map[string]interface{}{{"username": cred.UUID, "password": cred.Password}},
			})
		}
	}
	return result
}

func buildRelayInbound(input Input) interface{} {
	cred := input.NodeCreds[input.Node.Name]
	return map[string]interface{}{
		"type":        "socks",
		"tag":         "relay-socks5",
		"listen":      "::",
		"listen_port": input.Node.Spec.RelayPort,
		"users":       []map[string]interface{}{{"username": cred.Username, "password": cred.Password}},
	}
}

// buildOutboundNodeOutbounds creates SOCKS5 outbounds for region-auto-collected
// outbound nodes, skipping any node already covered by an explicit Route to
// avoid duplicate outbound entries with different tags.
func buildOutboundNodeOutbounds(input Input, myRoutes []*v1alpha1.ProxyRoute) []interface{} {
	routedNodes := make(map[string]bool, len(myRoutes))
	for _, r := range myRoutes {
		routedNodes[r.Spec.OutboundNode] = true
	}

	var result []interface{}
	for _, outNode := range input.OutboundNodes {
		if routedNodes[outNode.Name] {
			continue
		}
		cred := input.NodeCreds[outNode.Name]
		result = append(result, map[string]interface{}{
			"type":        "socks",
			"tag":         fmt.Sprintf("outbound-%s", outNode.Name),
			"server":      outNode.Spec.Address,
			"server_port": outNode.Spec.RelayPort,
			"username":    cred.Username,
			"password":    cred.Password,
		})
	}
	return result
}

// buildRouteOutbounds creates SOCKS5 outbounds for explicit ProxyRoutes.
// Uses tag "outbound-{outboundNodeName}" to match what buildRouteUserInbounds
// references in routing rules.
func buildRouteOutbounds(input Input, myRoutes []*v1alpha1.ProxyRoute) []interface{} {
	var result []interface{}
	for _, route := range myRoutes {
		outNode, ok := input.OutboundNodesByName[route.Spec.OutboundNode]
		if !ok {
			continue
		}
		cred := input.NodeCreds[outNode.Name]
		result = append(result, map[string]interface{}{
			"type":        "socks",
			"tag":         fmt.Sprintf("outbound-%s", outNode.Name),
			"server":      outNode.Spec.Address,
			"server_port": outNode.Spec.RelayPort,
			"username":    cred.Username,
			"password":    cred.Password,
		})
	}
	return result
}

func buildDirectOutbound() interface{} {
	return map[string]interface{}{
		"type": "direct",
		"tag":  "direct",
	}
}

func deduplicateByTag(outbounds []interface{}) []interface{} {
	seen := make(map[string]bool)
	var result []interface{}
	for _, ob := range outbounds {
		m, ok := ob.(map[string]interface{})
		if !ok {
			result = append(result, ob)
			continue
		}
		tag, _ := m["tag"].(string)
		if !seen[tag] {
			seen[tag] = true
			result = append(result, ob)
		}
	}
	return result
}

func routeFinal(outbounds []interface{}) string {
	for _, ob := range outbounds {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		if tag != "direct" && tag != "" {
			return tag
		}
	}
	return "direct"
}
