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
	EgressPolicies  []*v1alpha1.EgressPolicy
	UserCred        credmanager.UserCredential
	InboundNodes    []*v1alpha1.SingBoxNode
	RoutesByInbound map[string][]*v1alpha1.CustomRoute
	OutboundsByName map[string]*v1alpha1.SingBoxNode
	// ExternalOutboundsByName contains ExternalOutbound resources by name.
	// They participate in client configs only as names (tags, selector groups
	// and credential derivation) — clients always connect to the inbound node.
	ExternalOutboundsByName map[string]*v1alpha1.ExternalOutbound
	ExternalOutbounds       []*v1alpha1.ExternalOutbound
	// OfflineNodeNames contains SingBoxNode names that are currently offline
	// (NodeReady condition is False or absent). These nodes are excluded from
	// client config outbounds.
	OfflineNodeNames map[string]bool
	// AllowedNodeNames is the whitelist of relay outbound target names for
	// this user (from UserGroup.spec.allowedNodes); nil means allow all.
	// It only filters which outbounds an entry may relay through; it never
	// hides inbound nodes from the client config.
	AllowedNodeNames map[string]bool
	// DeniedNodeNames is the blacklist of relay outbound target names for
	// this user (from UserGroup.spec.deniedNodes); nil means deny none.
	// Outbound direction only, never hides inbound nodes.
	DeniedNodeNames map[string]bool
	// PolicyOnlyEgressNames are selected by EgressPolicy and intentionally are
	// not advertised as ordinary client-selectable outbounds.
	PolicyOnlyEgressNames map[string]bool
}

// BuildClientConfig generates the outbounds array for a client sing-box config.
// It emits regional selectors, the aggregate AI selector when needed, and direct.
func BuildClientConfig(input ClientConfigInput) ([]any, error) {
	policyOutbounds, _, targets, err := buildClientPolicies(input)
	if err != nil {
		return nil, err
	}
	// Never advertise a policy target in the ordinary regional pool.
	if len(targets) > 0 {
		input.PolicyOnlyEgressNames = maps.Clone(input.PolicyOnlyEgressNames)
		if input.PolicyOnlyEgressNames == nil {
			input.PolicyOnlyEgressNames = make(map[string]bool)
		}
		for name := range targets {
			if !targetHasClientGroup(input, name, "ai") {
				input.PolicyOnlyEgressNames[name] = true
			}
		}
	}
	var proxyOutbounds []any
	groupOutbounds := make(map[string][]string)

	for _, inboundNode := range input.InboundNodes {
		if input.OfflineNodeNames[inboundNode.Name] {
			continue
		}
		// Group allow/deny lists intentionally do NOT filter inbound nodes:
		// they only restrict relay outbound targets below.
		protocol := configengine.EffectiveInboundProtocol(inboundNode)
		if !supportsProtocol(inboundNode, protocol) {
			continue
		}

		address, port, ok := findEntryEndpoint(inboundNode.Status.EntryEndpoints, protocol)
		if !ok {
			continue
		}

		outboundNodes := resolveOutboundNodes(input, inboundNode.Name)

		for _, outboundNode := range outboundNodes {
			var tag string
			if outboundNode.Name == inboundNode.Name {
				tag = outboundNode.Name
			} else {
				tag = fmt.Sprintf("%s#%s", outboundNode.Name, inboundNode.Name)
			}
			ob := buildProxyOutbound(tag, address, port, protocol, input.User.Name, outboundNode.Name, inboundNode.Status.TLSServerName, input.UserCred)
			added := false
			for _, group := range outboundNode.ClientGroups {
				if group != "hk" && group != "jp" && group != "us" && group != "ai" {
					continue
				}
				if group != "ai" && input.PolicyOnlyEgressNames[outboundNode.Name] {
					continue
				}
				groupOutbounds[group] = append(groupOutbounds[group], tag)
				added = true
			}
			if added {
				proxyOutbounds = append(proxyOutbounds, ob)
			}
		}
	}

	var result []any
	result = append(result, proxyOutbounds...)

	// Emit only non-empty selectors. Unknown regions are intentionally omitted
	// rather than creating a mode that cannot be selected safely.
	groupTags := make([]string, 0, len(groupOutbounds))
	for k := range groupOutbounds {
		if len(groupOutbounds[k]) > 0 {
			groupTags = append(groupTags, k)
		}
	}
	sort.Strings(groupTags)

	// Emit one selector per normalized regional group.
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

	result = append(result, policyOutbounds...)
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

// outboundRef is a unified reference to an outbound target of an inbound node:
// either a SingBoxNode with the outbound role or an ExternalOutbound. Client
// configs only need the name (for tags, group selectors and credential
// derivation); AllowedInbounds carries the per-outbound inbound restriction.
// ClientGroups are the client selector groups for this target.
type outboundRef struct {
	Name            string
	AllowedInbounds []string
	ClientGroups    []string
}

func targetHasClientGroup(input ClientConfigInput, name, group string) bool {
	if n := input.OutboundsByName[name]; n != nil {
		return slices.Contains(n.Spec.ClientGroups, group)
	}
	if e := input.ExternalOutboundsByName[name]; e != nil {
		return slices.Contains(e.Spec.ClientGroups, group)
	}
	return false
}

func resolveOutboundNodes(input ClientConfigInput, inboundName string) []outboundRef {
	var inboundNode *v1alpha1.SingBoxNode
	for _, n := range input.InboundNodes {
		if n.Name == inboundName {
			inboundNode = n
			break
		}
	}

	seen := make(map[string]bool)
	var refs []outboundRef

	if inboundNode != nil {
		for _, n := range input.OutboundsByName {
			// Peers without a relay port produce no outbound entry in node
			// configs; advertising them would give clients broken paths.
			// Same-node (direct) outbounds are exempt via the self branch below.
			if n.Spec.RelayPort == 0 {
				continue
			}
			if n.Spec.Region == inboundNode.Spec.Region && !seen[n.Name] && (!input.PolicyOnlyEgressNames[n.Name] || targetHasClientGroup(input, n.Name, "ai")) && !input.OfflineNodeNames[n.Name] &&
				configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
				(len(n.Spec.AllowedInbounds) == 0 || slices.Contains(n.Spec.AllowedInbounds, inboundName)) &&
				(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
				seen[n.Name] = true
				refs = append(refs, outboundRef{Name: n.Name, AllowedInbounds: n.Spec.AllowedInbounds, ClientGroups: n.Spec.ClientGroups})
			}
		}
		if hasOutboundRole(inboundNode) && !seen[inboundNode.Name] && (!input.PolicyOnlyEgressNames[inboundNode.Name] || targetHasClientGroup(input, inboundNode.Name, "ai")) && !input.OfflineNodeNames[inboundNode.Name] &&
			configengine.IsNodeAllowed(inboundNode.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
			(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, inboundNode.Name)) {
			seen[inboundNode.Name] = true
			refs = append(refs, outboundRef{Name: inboundNode.Name, ClientGroups: inboundNode.Spec.ClientGroups})
		}
		// Same-region ExternalOutbounds are auto-discovered like outbound
		// SingBoxNodes, except that an empty region never auto-discovers and
		// offline filtering does not apply to them. A name already present as
		// an outbound SingBoxNode always wins over an ExternalOutbound.
		for _, eob := range input.ExternalOutboundsByName {
			if eob.Spec.Region != "" && eob.Spec.Region == inboundNode.Spec.Region && !seen[eob.Name] && (!input.PolicyOnlyEgressNames[eob.Name] || targetHasClientGroup(input, eob.Name, "ai")) &&
				input.OutboundsByName[eob.Name] == nil &&
				configengine.IsNodeAllowed(eob.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
				(len(eob.Spec.AllowedInbounds) == 0 || slices.Contains(eob.Spec.AllowedInbounds, inboundName)) &&
				(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, eob.Name)) {
				seen[eob.Name] = true
				refs = append(refs, outboundRef{Name: eob.Name, AllowedInbounds: eob.Spec.AllowedInbounds, ClientGroups: eob.Spec.ClientGroups})
			}
		}
	}

	if inboundNode != nil {
		for _, r := range input.RoutesByInbound[inboundName] {
			if r.EffectiveOutboundKind() == v1alpha1.OutboundKindExternalOutbound {
				if eob, ok := input.ExternalOutboundsByName[r.Spec.OutboundNode]; ok && eob.Spec.Region == inboundNode.Spec.Region && !seen[eob.Name] && (!input.PolicyOnlyEgressNames[eob.Name] || targetHasClientGroup(input, eob.Name, "ai")) &&
					input.OutboundsByName[eob.Name] == nil &&
					configengine.IsNodeAllowed(eob.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
					(len(eob.Spec.AllowedInbounds) == 0 || slices.Contains(eob.Spec.AllowedInbounds, inboundName)) &&
					(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, eob.Name)) {
					seen[eob.Name] = true
					refs = append(refs, outboundRef{Name: eob.Name, AllowedInbounds: eob.Spec.AllowedInbounds, ClientGroups: eob.Spec.ClientGroups})
				}
				continue
			}
			if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok && n.Spec.Region == inboundNode.Spec.Region && n.Spec.RelayPort != 0 && !seen[n.Name] && (!input.PolicyOnlyEgressNames[n.Name] || targetHasClientGroup(input, n.Name, "ai")) && !input.OfflineNodeNames[n.Name] &&
				configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
				(len(n.Spec.AllowedInbounds) == 0 || slices.Contains(n.Spec.AllowedInbounds, inboundName)) &&
				(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
				seen[n.Name] = true
				refs = append(refs, outboundRef{Name: n.Name, AllowedInbounds: n.Spec.AllowedInbounds, ClientGroups: n.Spec.ClientGroups})
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Name < refs[j].Name
	})
	return refs
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
