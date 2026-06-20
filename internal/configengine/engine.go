package configengine

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

const relayContainerPort = int32(10808)

// UserCredential holds the UUID that drives all per-protocol credential derivation.
type UserCredential struct {
	UUID string
}

// NodeCredential holds the SOCKS5 credentials for inter-node relay.
type NodeCredential struct {
	Username string
	Password string
}

// Input contains all data needed to compute a node's sing-box config.
type Input struct {
	Node                *v1alpha1.SingBoxNode
	Users               []*v1alpha1.User
	UserCreds           map[string]UserCredential
	OutboundNodes       []*v1alpha1.SingBoxNode
	Routes              []*v1alpha1.CustomRoute
	NodeCreds           map[string]NodeCredential
	OutboundNodesByName map[string]*v1alpha1.SingBoxNode

	UsageCollectionEnabled bool
	V2RayAPIListenAddr     string

	// UserNodeRestrictions maps userName → set of denied SingBoxNode names.
	// A nil or missing entry means no restrictions (allow all nodes).
	// Only the denied set is stored; the inbound pre-filter in the controller
	// handles allowlist checks before the config engine is called.
	UserNodeRestrictions map[string]map[string]bool
}

// Output contains the computed sing-box config.
type Output struct {
	Config []byte
	Hash   string
}

// singboxConfig mirrors the top-level sing-box config.json structure.
type singboxConfig struct {
	Log          logConfig           `json:"log"`
	Inbounds     []any               `json:"inbounds"`
	Outbounds    []any               `json:"outbounds"`
	Route        routeConfig         `json:"route"`
	Experimental *experimentalConfig `json:"experimental,omitempty"`
}

type experimentalConfig struct {
	V2RayAPI *v2rayAPIConfig `json:"v2ray_api,omitempty"`
}

type v2rayAPIConfig struct {
	Listen string           `json:"listen"`
	Stats  v2rayStatsConfig `json:"stats"`
}

type v2rayStatsConfig struct {
	Enabled bool     `json:"enabled"`
	Users   []string `json:"users"`
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
	isSelfOutbound := isInbound && isOutbound

	myRoutes := routesForNode(input)

	var inbounds []any
	var outbounds []any
	var rules []routeRule

	if isInbound {
		hasOutboundPeers := len(input.OutboundNodes) > 0 || len(myRoutes) > 0 || isSelfOutbound
		if hasOutboundPeers {
			ibs, rls := buildRouteInbounds(input, myRoutes, isSelfOutbound)
			inbounds = append(inbounds, ibs...)
			rules = rls
		} else {
			inbounds = append(inbounds, buildUserInbounds(input)...)
		}
		outbounds = append(outbounds, buildOutboundNodeOutbounds(input, myRoutes)...)
		outbounds = append(outbounds, buildRouteOutbounds(input, myRoutes)...)
		if isSelfOutbound {
			outbounds = append(outbounds, map[string]any{
				"type": "direct",
				"tag":  fmt.Sprintf("outbound-%s", node.Name),
			})
		}
	}

	if isOutbound {
		inbounds = append(inbounds, buildRelayInbound(input))
	}

	outbounds = deduplicateByTag(append(outbounds, buildDirectOutbound()))

	if inbounds == nil {
		inbounds = []any{}
	}

	var finalOutbound string
	if len(rules) > 0 {
		finalOutbound = "direct"
	} else {
		finalOutbound = routeFinal(outbounds)
	}

	var experimental *experimentalConfig
	if input.UsageCollectionEnabled {
		experimental = buildExperimentalConfig(input)
	}

	cfg := singboxConfig{
		Log:       logConfig{Level: "info"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route: routeConfig{
			Rules: rules,
			Final: finalOutbound,
		},
		Experimental: experimental,
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
func ExtractNodePorts(node *v1alpha1.SingBoxNode) []int32 {
	var ports []int32
	for _, p := range node.Spec.SupportedProtocols {
		ports = append(ports, p.Port)
	}
	if hasRole(node, v1alpha1.ProxyRoleOutbound) {
		ports = append(ports, relayContainerPort)
	}
	return ports
}

// IsNodeAllowed reports whether nodeName is permitted given the allow and deny sets.
//
// Semantics (deny-wins):
//  1. If deniedNodes[nodeName] is true  → return false
//  2. If len(allowedNodes) == 0          → return true (empty allowlist = allow all)
//  3. If allowedNodes[nodeName] is true  → return true
//  4. Otherwise                          → return false (not in allowlist)
//
// Nil maps are safe and treated as empty sets.
func IsNodeAllowed(nodeName string, allowedNodes, deniedNodes map[string]bool) bool {
	if deniedNodes[nodeName] {
		return false
	}
	if len(allowedNodes) == 0 {
		return true
	}
	return allowedNodes[nodeName]
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func hasRole(node *v1alpha1.SingBoxNode, role v1alpha1.ProxyRole) bool {
	return slices.Contains(node.Spec.Roles, role)
}

func findProtocolPort(node *v1alpha1.SingBoxNode, protocol string) int32 {
	for _, p := range node.Spec.SupportedProtocols {
		if p.Protocol == protocol {
			return p.Port
		}
	}
	return 0
}

func routesForNode(input Input) []*v1alpha1.CustomRoute {
	var result []*v1alpha1.CustomRoute
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

// DeriveUUID generates a deterministic UUID v5 from a base UUID and a suffix string.
// It implements the UUID v5 spec using SHA-1 with the base UUID as the namespace.
func DeriveUUID(baseUUID, suffix string) string {
	var stripped strings.Builder
	for _, c := range baseUUID {
		if c != '-' {
			stripped.WriteString(string(c))
		}
	}
	namespaceBytes, err := hex.DecodeString(stripped.String())
	if err != nil || len(namespaceBytes) != 16 {
		namespaceBytes = make([]byte, 16)
	}

	h := sha1.Sum(append(namespaceBytes, []byte(suffix)...))

	// UUID v5 version bits (RFC 4122 §4.3)
	h[6] = (h[6] & 0x0f) | 0x50
	// RFC 4122 variant bits
	h[8] = (h[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func virtualUserName(userName, outboundNodeName string) string {
	return fmt.Sprintf("%s#%s", userName, outboundNodeName)
}

func DerivePassword(baseUUID, suffix string) string {
	h := sha256.Sum256([]byte(baseUUID + "#" + suffix))
	return fmt.Sprintf("%x", h)[:32]
}

// DeriveAuth returns the sing-box auth fields for a given protocol, user UUID,
// and outbound node name. It is the single derivation point for all protocols.
func DeriveAuth(protocol, uuid, nodeName string) map[string]any {
	switch protocol {
	case "hysteria2", "trojan":
		return map[string]any{"password": DerivePassword(uuid, nodeName)}
	case "vless":
		return map[string]any{"uuid": DeriveUUID(uuid, nodeName)}
	case "socks5":
		return map[string]any{"username": uuid, "password": DerivePassword(uuid, nodeName)}
	case "http":
		return map[string]any{"username": uuid, "password": DerivePassword(uuid, nodeName)}
	default:
		return map[string]any{"password": DerivePassword(uuid, nodeName)}
	}
}

func buildRouteInbounds(input Input, routes []*v1alpha1.CustomRoute, includeSelf bool) ([]any, []routeRule) {
	var inbounds []any
	var rules []routeRule

	seen := make(map[string]bool)
	var outboundNames []string
	for _, n := range input.OutboundNodes {
		if !seen[n.Name] {
			seen[n.Name] = true
			outboundNames = append(outboundNames, n.Name)
		}
	}
	for _, r := range routes {
		if !seen[r.Spec.OutboundNode] {
			seen[r.Spec.OutboundNode] = true
			outboundNames = append(outboundNames, r.Spec.OutboundNode)
		}
	}
	if includeSelf && !seen[input.Node.Name] {
		seen[input.Node.Name] = true
		outboundNames = append(outboundNames, input.Node.Name)
	}

	for _, proto := range input.Node.Spec.SupportedProtocols {
		tag := fmt.Sprintf("inbound-%s", proto.Protocol)
		port := proto.Port

		var users []map[string]any
		for _, nodeName := range outboundNames {
			for _, user := range input.Users {
				if user.Spec.Protocol != proto.Protocol {
					continue
				}
				if !IsNodeAllowed(nodeName, nil, input.UserNodeRestrictions[user.Name]) {
					continue
				}
				cred := input.UserCreds[user.Name]
				vName := virtualUserName(user.Name, nodeName)
				auth := DeriveAuth(proto.Protocol, cred.UUID, nodeName)
				auth["name"] = vName
				users = append(users, auth)
			}
		}

		if len(users) == 0 {
			continue
		}

		inbounds = append(inbounds, buildInboundEntry(proto.Protocol, tag, port, users))
	}

	for _, nodeName := range outboundNames {
		outboundTag := fmt.Sprintf("outbound-%s", nodeName)
		var authUsers []string
		for _, user := range input.Users {
			if !IsNodeAllowed(nodeName, nil, input.UserNodeRestrictions[user.Name]) {
				continue
			}
			for _, proto := range input.Node.Spec.SupportedProtocols {
				if user.Spec.Protocol == proto.Protocol {
					authUsers = append(authUsers, virtualUserName(user.Name, nodeName))
					break
				}
			}
		}
		if len(authUsers) > 0 {
			rules = append(rules, routeRule{
				AuthUser: authUsers,
				Outbound: outboundTag,
			})
		}
	}

	return inbounds, rules
}

func buildUsersBlock(input Input, protocol, nodeName string) []map[string]any {
	var users []map[string]any
	for _, user := range input.Users {
		if user.Spec.Protocol != protocol {
			continue
		}
		// Defensive: skip users denied from the current inbound node.
		// Normally pre-filtered by the controller, but guard here as well.
		if !IsNodeAllowed(nodeName, nil, input.UserNodeRestrictions[user.Name]) {
			continue
		}
		cred := input.UserCreds[user.Name]
		auth := DeriveAuth(protocol, cred.UUID, nodeName)
		auth["name"] = user.Name
		users = append(users, auth)
	}
	return users
}

func buildInboundEntry(protocol, tag string, port int32, users []map[string]any) map[string]any {
	typeStr := protocol
	if protocol == "socks5" {
		typeStr = "socks"
	}
	entry := map[string]any{
		"type":        typeStr,
		"tag":         tag,
		"listen":      "::",
		"listen_port": port,
		"users":       users,
	}
	if protocol == "hysteria2" {
		entry["tls"] = map[string]any{
			"enabled":          true,
			"certificate_path": "/etc/sing-box/tls/tls.crt",
			"key_path":         "/etc/sing-box/tls/tls.key",
		}
	}
	return entry
}

func buildUserInbounds(input Input) []any {
	var result []any
	seen := make(map[string]bool)

	for _, user := range input.Users {
		proto := user.Spec.Protocol
		if seen[proto] {
			continue
		}
		seen[proto] = true

		port := findProtocolPort(input.Node, proto)
		tag := fmt.Sprintf("inbound-%s", proto)
		users := buildUsersBlock(input, proto, input.Node.Name)
		if len(users) == 0 {
			continue
		}
		result = append(result, buildInboundEntry(proto, tag, port, users))
	}
	return result
}

func buildRelayInbound(input Input) any {
	cred := input.NodeCreds[input.Node.Name]
	return map[string]any{
		"type":        "socks",
		"tag":         "relay-socks5",
		"listen":      "::",
		"listen_port": relayContainerPort,
		"users":       []map[string]any{{"username": cred.Username, "password": cred.Password}},
	}
}

func buildOutboundNodeOutbounds(input Input, myRoutes []*v1alpha1.CustomRoute) []any {
	routedNodes := make(map[string]bool, len(myRoutes))
	for _, r := range myRoutes {
		routedNodes[r.Spec.OutboundNode] = true
	}

	var result []any
	for _, outNode := range input.OutboundNodes {
		if routedNodes[outNode.Name] {
			continue
		}
		if outNode.Spec.RelayPort == 0 {
			continue
		}
		cred := input.NodeCreds[outNode.Name]
		result = append(result, map[string]any{
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

func buildRouteOutbounds(input Input, myRoutes []*v1alpha1.CustomRoute) []any {
	var result []any
	for _, route := range myRoutes {
		outNode, ok := input.OutboundNodesByName[route.Spec.OutboundNode]
		if !ok {
			continue
		}
		if outNode.Spec.RelayPort == 0 {
			continue
		}
		// Skip outbound entries where every user is denied from this node.
		if len(input.Users) > 0 {
			allDenied := true
			for _, user := range input.Users {
				if IsNodeAllowed(outNode.Name, nil, input.UserNodeRestrictions[user.Name]) {
					allDenied = false
					break
				}
			}
			if allDenied {
				continue
			}
		}
		cred := input.NodeCreds[outNode.Name]
		result = append(result, map[string]any{
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

func buildDirectOutbound() any {
	return map[string]any{
		"type": "direct",
		"tag":  "direct",
	}
}

func deduplicateByTag(outbounds []any) []any {
	seen := make(map[string]bool)
	var result []any
	for _, ob := range outbounds {
		m, ok := ob.(map[string]any)
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

func buildExperimentalConfig(input Input) *experimentalConfig {
	listenAddr := input.V2RayAPIListenAddr
	if listenAddr == "" {
		listenAddr = "0.0.0.0:10085"
	}

	seen := make(map[string]bool)
	var outboundNames []string
	for _, n := range input.OutboundNodes {
		if !seen[n.Name] {
			seen[n.Name] = true
			outboundNames = append(outboundNames, n.Name)
		}
	}
	for _, r := range routesForNode(input) {
		if !seen[r.Spec.OutboundNode] {
			seen[r.Spec.OutboundNode] = true
			outboundNames = append(outboundNames, r.Spec.OutboundNode)
		}
	}
	isInbound := hasRole(input.Node, v1alpha1.ProxyRoleInbound)
	isOutbound := hasRole(input.Node, v1alpha1.ProxyRoleOutbound)
	if isInbound && isOutbound && !seen[input.Node.Name] {
		seen[input.Node.Name] = true
		outboundNames = append(outboundNames, input.Node.Name)
	}

	var userSet = make(map[string]bool)
	for _, nodeName := range outboundNames {
		for _, user := range input.Users {
			for _, proto := range input.Node.Spec.SupportedProtocols {
				if user.Spec.Protocol == proto.Protocol {
					vName := virtualUserName(user.Name, nodeName)
					userSet[vName] = true
					break
				}
			}
		}
	}

	var statsUsers []string
	for name := range userSet {
		statsUsers = append(statsUsers, name)
	}
	sort.Strings(statsUsers)

	return &experimentalConfig{
		V2RayAPI: &v2rayAPIConfig{
			Listen: listenAddr,
			Stats: v2rayStatsConfig{
				Enabled: true,
				Users:   statsUsers,
			},
		},
	}
}

func routeFinal(outbounds []any) string {
	for _, ob := range outbounds {
		m, ok := ob.(map[string]any)
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

