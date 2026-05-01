package apiserver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// ClientConfigInput contains all data needed to generate client config
type ClientConfigInput struct {
	User            *v1alpha1.ProxyUser
	UserCred        credmanager.UserCredential
	InboundNodes    []*v1alpha1.ProxyNode
	RoutesByInbound map[string][]*v1alpha1.ProxyRoute
	OutboundsByName map[string]*v1alpha1.ProxyNode
}

// BuildClientConfig generates the outbounds array for a client sing-box config.
// Returns: proxy outbounds + selector("proxy") + direct
func BuildClientConfig(input ClientConfigInput) ([]interface{}, error) {
	protocol := input.User.Spec.Protocol

	var proxyOutbounds []interface{}
	var proxyTags []string

	for _, inboundNode := range input.InboundNodes {
		if !supportsProtocol(inboundNode, protocol) {
			continue
		}

		address, port, ok := findEntryEndpoint(inboundNode.Status.EntryEndpoints, protocol)
		if !ok {
			continue
		}

		outboundNodes := resolveOutboundNodes(input, inboundNode.Name)

		for _, outboundNode := range outboundNodes {
			tag := fmt.Sprintf("%s-%s", inboundNode.Name, outboundNode.Name)
			ob := buildProxyOutbound(tag, address, port, protocol, outboundNode.Name, input.UserCred)
			proxyOutbounds = append(proxyOutbounds, ob)
			proxyTags = append(proxyTags, tag)
		}
	}

	var result []interface{}
	result = append(result, proxyOutbounds...)
	result = append(result, map[string]interface{}{
		"type":      "selector",
		"tag":       "proxy",
		"outbounds": proxyTags,
	})
	result = append(result, map[string]interface{}{
		"type": "direct",
		"tag":  "direct",
	})

	return result, nil
}

func supportsProtocol(node *v1alpha1.ProxyNode, protocol string) bool {
	for _, p := range node.Spec.SupportedProtocols {
		if p.Protocol == protocol {
			return true
		}
	}
	return false
}

func findEntryEndpoint(endpoints []string, protocol string) (address string, port int, ok bool) {
	for _, ep := range endpoints {
		parts := strings.SplitN(ep, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] != protocol {
			continue
		}
		p, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		return parts[1], p, true
	}
	return "", 0, false
}

func resolveOutboundNodes(input ClientConfigInput, inboundName string) []*v1alpha1.ProxyNode {
	routes, hasRoutes := input.RoutesByInbound[inboundName]
	if hasRoutes && len(routes) > 0 {
		var nodes []*v1alpha1.ProxyNode
		for _, r := range routes {
			if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok {
				nodes = append(nodes, n)
			}
		}
		return nodes
	}

	var inboundNode *v1alpha1.ProxyNode
	for _, n := range input.InboundNodes {
		if n.Name == inboundName {
			inboundNode = n
			break
		}
	}
	if inboundNode == nil {
		return nil
	}

	var nodes []*v1alpha1.ProxyNode
	for _, n := range input.OutboundsByName {
		if n.Spec.Region == inboundNode.Spec.Region {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func buildProxyOutbound(tag, address string, port int, protocol, outboundNodeName string, cred credmanager.UserCredential) map[string]interface{} {
	ob := map[string]interface{}{
		"tag":         tag,
		"server":      address,
		"server_port": port,
	}

	switch protocol {
	case "vless":
		ob["type"] = "vless"
		ob["uuid"] = configengine.DeriveUUID(cred.UUID, outboundNodeName)
	case "trojan":
		ob["type"] = "trojan"
		ob["password"] = configengine.DerivePassword(cred.Password, outboundNodeName)
	case "socks5":
		ob["type"] = "socks"
		ob["username"] = cred.Username
		ob["password"] = configengine.DerivePassword(cred.Password, outboundNodeName)
	case "http":
		ob["type"] = "http"
		ob["username"] = cred.Username
		ob["password"] = configengine.DerivePassword(cred.Password, outboundNodeName)
	}

	return ob
}
