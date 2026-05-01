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
	Inbound  []string `json:"inbound,omitempty"`
	AuthUser []string `json:"auth_user,omitempty"`
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

	myRoutes := routesForNode(input)

	var inbounds []interface{}
	var outbounds []interface{}
	var rules []routeRule

	if isInbound {
		if len(myRoutes) > 0 {
			ibs, rls := buildRouteInbounds(input, myRoutes)
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

	outbounds = deduplicateByTag(append(outbounds, buildDirectOutbound()))

	if inbounds == nil {
		inbounds = []interface{}{}
	}

	cfg := singboxConfig{
		Log:       logConfig{Level: "info"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route: routeConfig{
			Rules: rules,
			Final: routeFinal(outbounds),
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

// buildRouteInbounds generates one inbound listener per protocol per route.
// Each inbound contains ALL users that use that protocol, so users share the
// same port per route. Port = basePort + routeIdx*numProtocols + protoIdx.
//
// Routing rules use auth_user (matched by user name) + inbound_tag to bind
// each (user, route) combination to the correct outbound.
//
// Example: protocols=[vless:30080, trojan:30081], routes=[to-acck, to-xtom]:
//
//	inbound-vless-to-acck  port=30080  users=[alice,bob]  → outbound-acck-jp
//	inbound-trojan-to-acck port=30081  users=[carol]      → outbound-acck-jp
//	inbound-vless-to-xtom  port=30082  users=[alice,bob]  → outbound-xtom-jp
//	inbound-trojan-to-xtom port=30083  users=[carol]      → outbound-xtom-jp
func buildRouteInbounds(input Input, routes []*v1alpha1.ProxyRoute) ([]interface{}, []routeRule) {
	numProtocols := len(input.Node.Spec.SupportedProtocols)
	if numProtocols == 0 {
		numProtocols = 1
	}

	var inbounds []interface{}
	var rules []routeRule

	for routeIdx, route := range routes {
		outboundTag := fmt.Sprintf("outbound-%s", route.Spec.OutboundNode)

		for protoIdx, proto := range input.Node.Spec.SupportedProtocols {
			port := proto.Port + int32(routeIdx*numProtocols+protoIdx)
			tag := fmt.Sprintf("inbound-%s-%s", proto.Protocol, route.Spec.OutboundNode)

			users := buildUsersBlock(input, proto.Protocol)
			if len(users) == 0 {
				continue
			}

			inbounds = append(inbounds, buildInboundEntry(proto.Protocol, tag, port, users))

			rules = append(rules, routeRule{
				Inbound:  []string{tag},
				AuthUser: collectUserNames(input, proto.Protocol),
				Outbound: outboundTag,
			})
		}
	}

	return inbounds, rules
}

func buildUsersBlock(input Input, protocol string) []map[string]interface{} {
	var users []map[string]interface{}
	for _, user := range input.Users {
		if user.Spec.Protocol != protocol {
			continue
		}
		cred := input.UserCreds[user.Name]
		switch protocol {
		case "vless":
			users = append(users, map[string]interface{}{
				"name": user.Name,
				"uuid": cred.UUID,
			})
		case "trojan":
			users = append(users, map[string]interface{}{
				"name":     user.Name,
				"password": cred.Password,
			})
		case "socks5":
			users = append(users, map[string]interface{}{
				"name":     user.Name,
				"username": cred.Username,
				"password": cred.Password,
			})
		case "http":
			users = append(users, map[string]interface{}{
				"name":     user.Name,
				"username": cred.UUID,
				"password": cred.Password,
			})
		}
	}
	return users
}

func collectUserNames(input Input, protocol string) []string {
	var names []string
	for _, user := range input.Users {
		if user.Spec.Protocol == protocol {
			names = append(names, user.Name)
		}
	}
	return names
}

func buildInboundEntry(protocol, tag string, port int32, users []map[string]interface{}) map[string]interface{} {
	typeStr := protocol
	if protocol == "socks5" {
		typeStr = "socks"
	}
	return map[string]interface{}{
		"type":        typeStr,
		"tag":         tag,
		"listen":      "::",
		"listen_port": port,
		"users":       users,
	}
}

func buildUserInbounds(input Input) []interface{} {
	var result []interface{}
	seen := make(map[string]bool)

	for _, user := range input.Users {
		proto := user.Spec.Protocol
		if seen[proto] {
			continue
		}
		seen[proto] = true

		port := findProtocolPort(input.Node, proto)
		tag := fmt.Sprintf("inbound-%s", proto)
		users := buildUsersBlock(input, proto)
		if len(users) == 0 {
			continue
		}
		result = append(result, buildInboundEntry(proto, tag, port, users))
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
