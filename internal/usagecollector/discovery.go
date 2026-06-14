package usagecollector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CollectTarget describes a single sing-box node from which the usage
// collector pulls traffic stats. Each target corresponds to one inbound
// SingBoxNode.
type CollectTarget struct {
	// NodeName is the SingBoxNode resource name.
	NodeName string

	// V2RayAPIAddr is the host:port of the node's v2ray API gRPC endpoint.
	V2RayAPIAddr string

	// VirtualUsers is the list of virtual user names that may appear in
	// this node's traffic counters. Each entry has the form
	// "{userName}#{outboundNodeName}".
	VirtualUsers []string
}

// Discoverer discovers CollectTargets that the usage collector should poll.
// Implementations must be safe for (at least) sequential calls across
// discovery cycles.
type Discoverer interface {
	// Discover returns the current set of CollectTargets in the cluster.
	// It must not mutate any state on the Discoverer itself (idempotent).
	Discover(ctx context.Context) ([]CollectTarget, error)
}

// K8sDiscoverer implements Discoverer by listing SingBoxNode, User, and
// CustomRoute objects from a Kubernetes API server via controller-runtime's
// client.Client.
type K8sDiscoverer struct {
	client    client.Client
	reader    client.Reader
	namespace string
	log       logr.Logger
}

func NewK8sDiscoverer(c client.Client, reader client.Reader, namespace string) *K8sDiscoverer {
	return &K8sDiscoverer{
		client:    c,
		reader:    reader,
		namespace: namespace,
		log:       log.Log.WithName("usagecollector.discovery"),
	}
}

// Discover implements Discoverer.
func (d *K8sDiscoverer) Discover(ctx context.Context) ([]CollectTarget, error) {
	// List all SingBoxNodes in the namespace.
	allNodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := d.client.List(ctx, allNodes, client.InNamespace(d.namespace)); err != nil {
		return nil, fmt.Errorf("listing SingBoxNodes: %w", err)
	}
	sort.Slice(allNodes.Items, func(i, j int) bool {
		return allNodes.Items[i].Name < allNodes.Items[j].Name
	})

	// Filter to inbound-role nodes.
	inboundNodes := make([]*proxyv1alpha1.SingBoxNode, 0, len(allNodes.Items))
	for i := range allNodes.Items {
		n := &allNodes.Items[i]
		if hasRole(n, proxyv1alpha1.ProxyRoleInbound) {
			inboundNodes = append(inboundNodes, n)
		}
	}

	d.log.Info("Found inbound nodes", "count", len(inboundNodes))

	if len(inboundNodes) == 0 {
		return []CollectTarget{}, nil
	}

	// List all CustomRoutes to determine which outbound nodes each inbound
	// node routes to.
	allRoutes := &proxyv1alpha1.CustomRouteList{}
	if err := d.client.List(ctx, allRoutes, client.InNamespace(d.namespace)); err != nil {
		return nil, fmt.Errorf("listing CustomRoutes: %w", err)
	}

	// Build map: inboundNodeName → set of outboundNodeNames
	routesByInbound := make(map[string]map[string]bool)
	for i := range allRoutes.Items {
		r := &allRoutes.Items[i]
		if routesByInbound[r.Spec.InboundNode] == nil {
			routesByInbound[r.Spec.InboundNode] = make(map[string]bool)
		}
		routesByInbound[r.Spec.InboundNode][r.Spec.OutboundNode] = true
	}

	// Find all outbound node names (same region as inbound) that aren't
	// already covered by explicit routes. The config engine matches outbound
	// nodes with the same region as fallback.
	// Build a map of region→outbound node names.
	outboundByRegion := make(map[string][]string)
	for i := range allNodes.Items {
		n := &allNodes.Items[i]
		if hasRole(n, proxyv1alpha1.ProxyRoleOutbound) {
			region := n.Spec.Region
			if region == "" {
				region = "default"
			}
			outboundByRegion[region] = append(outboundByRegion[region], n.Name)
		}
	}

	// List all Users in the namespace.
	allUsers := &proxyv1alpha1.UserList{}
	if err := d.client.List(ctx, allUsers, client.InNamespace(d.namespace)); err != nil {
		return nil, fmt.Errorf("listing Users: %w", err)
	}
	sort.Slice(allUsers.Items, func(i, j int) bool {
		return allUsers.Items[i].Name < allUsers.Items[j].Name
	})

	// Build the result.
	targets := make([]CollectTarget, 0, len(inboundNodes))
	for _, in := range inboundNodes {
		// Determine outbound node names for this inbound node.
		outboundNames := make(map[string]bool)

		// 1. Explicit routes.
		if explicit, ok := routesByInbound[in.Name]; ok {
			for name := range explicit {
				outboundNames[name] = true
			}
		}

		// 2. Same-region outbound nodes (fallback routing).
		region := in.Spec.Region
		if region == "" {
			region = "default"
		}
		for _, name := range outboundByRegion[region] {
			outboundNames[name] = true
		}

		// Sort outbound names for deterministic output.
		sortedOutbound := make([]string, 0, len(outboundNames))
		for name := range outboundNames {
			sortedOutbound = append(sortedOutbound, name)
		}
		sort.Strings(sortedOutbound)

		// Compute virtual users: all user names × outbound node names.
		virtualUsers := make([]string, 0, len(allUsers.Items)*len(sortedOutbound))
		for _, user := range allUsers.Items {
			for _, outName := range sortedOutbound {
				virtualUsers = append(virtualUsers, virtualUserName(user.Name, outName))
			}
		}
		sort.Strings(virtualUsers)

		v2rayAddr := d.resolveV2RayAPIAddr(ctx, in)

		d.log.Info("Built collect target",
			"node", in.Name,
			"addr", v2rayAddr,
			"region", in.Spec.Region,
			"virtualUsers", len(virtualUsers),
			"outboundNames", sortedOutbound,
		)

		targets = append(targets, CollectTarget{
			NodeName:     in.Name,
			V2RayAPIAddr: v2rayAddr,
			VirtualUsers: virtualUsers,
		})
	}

	return targets, nil
}

func (d *K8sDiscoverer) resolveV2RayAPIAddr(ctx context.Context, node *proxyv1alpha1.SingBoxNode) string {
	podList := &corev1.PodList{}
	if err := d.reader.List(ctx, podList,
		client.InNamespace(node.Namespace),
		client.MatchingLabels{"app": "singbox", "singboxnode": node.Name},
	); err != nil {
		d.log.Error(err, "Failed to list pods for node, falling back to Spec.Address", "node", node.Name)
	} else {
		d.log.Info("Found pods for node", "node", node.Name, "count", len(podList.Items))
		for i := range podList.Items {
			pod := &podList.Items[i]
			d.log.Info("Pod candidate", "node", node.Name, "pod", pod.Name, "phase", pod.Status.Phase, "podIP", pod.Status.PodIP)
			if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
				addr := fmt.Sprintf("%s:10085", pod.Status.PodIP)
				d.log.Info("Resolved v2rayapi addr via pod IP", "node", node.Name, "addr", addr)
				return addr
			}
		}
	}
	fallback := fmt.Sprintf("%s:10085", node.Spec.Address)
	d.log.Info("Falling back to Spec.Address for v2rayapi addr", "node", node.Name, "addr", fallback)
	return fallback
}

// virtualUserName returns the sing-box virtual user name for a user on a
// specific outbound node. This matches the configengine.virtualUserName
// convention.
func virtualUserName(userName, outboundNodeName string) string {
	return fmt.Sprintf("%s#%s", userName, outboundNodeName)
}

// hasRole checks whether a SingBoxNode has the given role.
func hasRole(node *proxyv1alpha1.SingBoxNode, role proxyv1alpha1.ProxyRole) bool {
	for _, r := range node.Spec.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// normalizeCounterName extracts the user name, node name, and direction
// from a sing-box v2ray API counter name.
//
// Expected format:
//
//	user>>>{userName}#{outboundNodeName}>>>traffic>>>{uplink|downlink}
//
// Returns ("", "", "", false) if the counter name does not match.
func normalizeCounterName(counter string) (user, node, direction string, ok bool) {
	// Expected parts: ["user", "{name}#{node}", "traffic", "{uplink|downlink}"]
	parts := strings.Split(counter, ">>>")
	if len(parts) != 4 {
		return "", "", "", false
	}
	if parts[0] != "user" {
		return "", "", "", false
	}
	if parts[2] != "traffic" {
		return "", "", "", false
	}

	// Parse {name}#{node} — expect exactly one hash separator.
	virtualUser := parts[1]
	hashIdx := strings.LastIndex(virtualUser, "#")
	if hashIdx <= 0 || hashIdx == len(virtualUser)-1 {
		return "", "", "", false
	}
	userName := virtualUser[:hashIdx]
	nodeName := virtualUser[hashIdx+1:]

	direction = parts[3]
	if direction != "uplink" && direction != "downlink" {
		return "", "", "", false
	}

	return userName, nodeName, direction, true
}

// NormalizeCounterToRecord converts a parsed counter name and its delta
// value into a UsageRecord. Returns ok=false when the counter name does
// not map to a known user/node pattern (e.g. non-user counters like
// "inbound>>>some>>>counter").
func NormalizeCounterToRecord(counterName string, delta int64, collectedAt time.Time) (UsageRecord, bool) {
	user, node, direction, ok := normalizeCounterName(counterName)
	if !ok {
		return UsageRecord{}, false
	}

	record := UsageRecord{
		Timestamp:   collectedAt,
		User:        user,
		Node:        node,
		CollectedAt: collectedAt,
	}
	if direction == "uplink" {
		record.UplinkBytes = delta
	} else {
		record.DownlinkBytes = delta
	}
	return record, true
}
