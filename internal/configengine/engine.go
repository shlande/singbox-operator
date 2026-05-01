package configengine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/your-org/singbox-operator/api/v1alpha1"
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

type routeConfig struct {
	Final string `json:"final"`
}

// Compute calculates the complete sing-box config.json for a given ProxyNode.
func Compute(input Input) (Output, error) {
	node := input.Node

	isInbound := hasRole(node, v1alpha1.ProxyRoleInbound)
	isOutbound := hasRole(node, v1alpha1.ProxyRoleOutbound)

	var inbounds []interface{}
	var outbounds []interface{}

	if isInbound {
		inbounds = append(inbounds, buildUserInbounds(input)...)
	}

	if isOutbound {
		inbounds = append(inbounds, buildRelayInbound(input))
	}

	if isInbound {
		outbounds = append(outbounds, buildOutboundNodeOutbounds(input)...)
		outbounds = append(outbounds, buildRouteOutbounds(input)...)
	}

	// Always append direct; deduplicate by tag
	outbounds = deduplicateByTag(append(outbounds, buildDirectOutbound()))

	// Ensure inbounds is always an initialized slice (not nil)
	if inbounds == nil {
		inbounds = []interface{}{}
	}

	final := routeFinal(outbounds)

	cfg := singboxConfig{
		Log:       logConfig{Level: "info"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route:     routeConfig{Final: final},
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

func buildOutboundNodeOutbounds(input Input) []interface{} {
	var result []interface{}
	for _, outNode := range input.OutboundNodes {
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

func buildRouteOutbounds(input Input) []interface{} {
	var result []interface{}
	for _, route := range input.Routes {
		if route.Spec.InboundNode != input.Node.Name {
			continue
		}
		outNode, ok := input.OutboundNodesByName[route.Spec.OutboundNode]
		if !ok {
			continue
		}
		cred := input.NodeCreds[outNode.Name]
		result = append(result, map[string]interface{}{
			"type":        "socks",
			"tag":         fmt.Sprintf("route-%s", route.Name),
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
