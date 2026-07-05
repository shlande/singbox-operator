package apiserver

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// ClientConfigInput contains all data needed to generate client config
type ClientConfigInput struct {
	User            *v1alpha1.User
	UserCred        credmanager.UserCredential
	InboundNodes    []*v1alpha1.SingBoxNode
	RoutesByInbound map[string][]*v1alpha1.CustomRoute
	OutboundsByName map[string]*v1alpha1.SingBoxNode
	// OfflineNodeNames contains SingBoxNode names that are currently offline
	// (NodeReady condition is False or absent). These nodes are excluded from
	// client config outbounds.
	OfflineNodeNames map[string]bool
	// AllowedNodeNames is the whitelist of SingBoxNode names for this user (from UserGroup).
	// nil means allow all.
	AllowedNodeNames map[string]bool
	// DeniedNodeNames is the blacklist of SingBoxNode names for this user (from UserGroup).
	// nil means deny none.
	DeniedNodeNames map[string]bool
}

// BuildClientConfig generates the outbounds array for a client sing-box config.
// Returns: proxy outbounds + per-inbound-tag selectors + selector("proxy") + direct
func BuildClientConfig(input ClientConfigInput) ([]any, error) {
	var proxyOutbounds []any
	groupOutbounds := make(map[string][]string)

	for _, inboundNode := range input.InboundNodes {
		if input.OfflineNodeNames[inboundNode.Name] {
			continue
		}
		if !configengine.IsNodeAllowed(inboundNode.Name, input.AllowedNodeNames, input.DeniedNodeNames) {
			continue
		}
		protocol := configengine.EffectiveInboundProtocol(inboundNode)
		if !supportsProtocol(inboundNode, protocol) {
			continue
		}

		address, port, ok := findEntryEndpoint(inboundNode.Status.EntryEndpoints, protocol)
		if !ok {
			continue
		}

		outboundNodes := resolveOutboundNodes(input, inboundNode.Name)

		var inboundTags []string
		for _, outboundNode := range outboundNodes {
			var tag string
			if outboundNode.Name == inboundNode.Name {
				tag = outboundNode.Name
			} else {
				tag = fmt.Sprintf("%s#%s", outboundNode.Name, inboundNode.Name)
			}
			ob := buildProxyOutbound(tag, address, port, protocol, input.User.Name, outboundNode.Name, inboundNode.Status.TLSServerName, input.UserCred)
			proxyOutbounds = append(proxyOutbounds, ob)
			inboundTags = append(inboundTags, tag)
		}

		// Only record a group if the inbound produced at least one proxy outbound
		if len(inboundTags) > 0 {
			tag := inboundNode.Spec.Tag
			if tag == "" {
				tag = "default"
			}
			groupOutbounds[tag] = append(groupOutbounds[tag], inboundTags...)
		}
	}

	var result []any
	result = append(result, proxyOutbounds...)

	// Sort group tags in dictionary order
	groupTags := make([]string, 0, len(groupOutbounds))
	for k := range groupOutbounds {
		groupTags = append(groupTags, k)
	}
	sort.Strings(groupTags)

	result = append(result, map[string]any{
		"type":      "selector",
		"tag":       "proxy",
		"outbounds": groupTags,
	})

	// Emit one selector per group tag
	for _, gt := range groupTags {
		tags := groupOutbounds[gt]
		sort.Strings(tags)
		tags = slices.Compact(tags)
		result = append(result, map[string]any{
			"type":      "selector",
			"tag":       gt,
			"outbounds": tags,
		})
	}

	result = append(result, map[string]any{
		"type": "direct",
		"tag":  "direct",
	})

	return result, nil
}

func supportsProtocol(node *v1alpha1.SingBoxNode, protocol string) bool {
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

func resolveOutboundNodes(input ClientConfigInput, inboundName string) []*v1alpha1.SingBoxNode {
	var inboundNode *v1alpha1.SingBoxNode
	for _, n := range input.InboundNodes {
		if n.Name == inboundName {
			inboundNode = n
			break
		}
	}

	seen := make(map[string]bool)
	var nodes []*v1alpha1.SingBoxNode

	if inboundNode != nil {
		for _, n := range input.OutboundsByName {
			if n.Spec.Region == inboundNode.Spec.Region && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
				configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
				(len(n.Spec.AllowedInbounds) == 0 || slices.Contains(n.Spec.AllowedInbounds, inboundName)) &&
				(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
				seen[n.Name] = true
				nodes = append(nodes, n)
			}
		}
		if hasOutboundRole(inboundNode) && !seen[inboundNode.Name] && !input.OfflineNodeNames[inboundNode.Name] &&
			configengine.IsNodeAllowed(inboundNode.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
			(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, inboundNode.Name)) {
			seen[inboundNode.Name] = true
			nodes = append(nodes, inboundNode)
		}
	}

	if inboundNode != nil {
		for _, r := range input.RoutesByInbound[inboundName] {
			if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
				configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
				(len(n.Spec.AllowedInbounds) == 0 || slices.Contains(n.Spec.AllowedInbounds, inboundName)) &&
				(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
				seen[n.Name] = true
				nodes = append(nodes, n)
			}
		}
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

func hasOutboundRole(node *v1alpha1.SingBoxNode) bool {
	return slices.Contains(node.Spec.Roles, v1alpha1.ProxyRoleOutbound)
}

func buildProxyOutbound(tag, address string, port int, protocol, userName, outboundNodeName, tlsServerName string, cred credmanager.UserCredential) map[string]any {
	typeStr := protocol
	if protocol == "socks5" {
		typeStr = "socks"
	}
	ob := map[string]any{
		"type":        typeStr,
		"tag":         tag,
		"server":      address,
		"server_port": port,
	}
	maps.Copy(ob, configengine.DeriveAuth(protocol, cred.UUID, outboundNodeName))
	// For naive protocol, the inbound server identifies users by "user#node" format username.
	// Override the uuid-based username from DeriveAuth to match the server's expectation.
	if protocol == "naive" {
		ob["username"] = fmt.Sprintf("%s#%s", userName, outboundNodeName)
	}
	if protocol == "hysteria2" || protocol == "naive" || protocol == "anytls" || protocol == "tuic" {
		tls := map[string]any{"enabled": true}
		if tlsServerName != "" {
			tls["server_name"] = tlsServerName
		} else {
			tls["insecure"] = true
		}
		ob["tls"] = tls
	}
	return ob
}
