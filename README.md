# sing-box-operator-2

A Kubernetes operator for managing [sing-box](https://github.com/SagerNet/sing-box) proxy nodes as Kubernetes custom resources. The operator deploys, configures, and connects proxy nodes with inbound (client-facing protocols) and outbound (upstream exit) roles across regions.

## Description

The sing-box-operator-2 manages a fleet of sing-box proxy nodes via the `SingBoxNode` custom resource. Each node can act as an **inbound** (accepting client connections via hysteria2, vless, trojan, etc.), an **outbound** (forwarding traffic upstream), or both. The operator generates sing-box configuration files automatically, handles TLS certificates, and applies destination routing through `EgressPolicy` resources. A key access-control feature is **AllowedInbounds** (outbound-side whitelist of permitted inbounds) and **AllowedOutbounds** (inbound-side whitelist of permitted outbounds), which restrict node-to-node routing.

## Features

### Node Roles

Each `SingBoxNode` can be assigned one or both roles:
- **inbound** — accepts client connections on supported protocols (hysteria2, vless, trojan, socks5, http, naive, anytls, tuic)
- **outbound** — forwards traffic upstream; receives relay connections from inbound nodes

Nodes can span multiple geographic regions; inbound nodes automatically discover same-region outbound nodes via region matching.

### AllowedInbounds

`AllowedInbounds` is an optional field on outbound nodes that restricts which inbound nodes may use this node as an outbound:

- **Empty or omitted** — allow all inbound nodes (backward compatible)
- **Non-empty** — only inbound nodes whose names appear in the list may use this outbound

Example:

```yaml
apiVersion: singboxoperator.shlande.top/v1alpha1
kind: SingBoxNode
metadata:
  name: us-west-outbound
spec:
  nodeRef: node-3
  address: 203.0.113.10
  region: us-west
  roles:
    - outbound
  relayPort: 31970
  allowedInbounds:
    - "us-west-inbound-a"
    - "us-west-inbound-b"
```

In this example, only `us-west-inbound-a` and `us-west-inbound-b` may use this node as their outbound. All other inbound nodes — even in the same region — are blocked.

### EgressPolicy

`EgressPolicy` applies ordered sing-box route rules to all inbound nodes selected by `spec.ingressSelector`. Selectors use Kubernetes label-selector semantics and support `matchNames` as an additional exact-name constraint. A `route` action must select exactly one outbound `SingBoxNode` or `ExternalOutbound`; zero or multiple matches set the policy `Degraded` and do not generate a route. `reject` does not need an egress selector.

Rules support `domain`, `domainSuffix`, `domainRegex`, `ipCIDR`, and `ruleSet` matching. Lower `priority` values run first; equal priorities are ordered by policy name. Set `fallbackAction: reject` to reject unmatched traffic using the policy egress.

```yaml
apiVersion: singboxoperator.shlande.top/v1alpha1
kind: EgressPolicy
metadata:
  name: route-example-domains
spec:
  ingressSelector:
    matchLabels:
      role: ingress
  egressSelector:
    matchNames: [us-west-outbound]
  match:
    domainSuffix:
      - example.com
  action: route
  fallbackAction: reject
  priority: 100
```

### AllowedOutbounds

`AllowedOutbounds` is an optional field on inbound nodes that restricts which outbound nodes they may use as upstream:

- **Empty or omitted** — allow all same-region outbounds (backward compatible)
- **Non-empty** — exclusive whitelist: only outbounds whose names appear in the list are usable. This gates same-region auto-discovery; EgressPolicy targets are selected independently by policy selectors.

Example:

```yaml
apiVersion: singboxoperator.shlande.top/v1alpha1
kind: SingBoxNode
metadata:
  name: us-west-inbound-a
spec:
  nodeRef: node-1
  address: 203.0.113.5
  region: us-west
  roles:
    - inbound
  allowedOutbounds:
    - "us-west-outbound"
```

In this example, `us-west-inbound-a` may only use `us-west-outbound` as its upstream, even if other outbound nodes exist in the `us-west` region. Same-region auto-discovery is suppressed; only the whitelisted outbound is used.

### AllowedOutbounds interaction with AllowedInbounds

When both `AllowedOutbounds` (on inbound A) and `AllowedInbounds` (on outbound B) are set, automatic same-region pairing is an AND gate: A may use B only if `A.Spec.AllowedOutbounds` includes B AND `B.Spec.AllowedInbounds` includes A (or is empty).

### Self-as-outbound

When a node has both inbound and outbound roles, setting `allowedOutbounds: ["<self-name>"]` restricts it to use only itself as upstream. For example, a node named `dual-role-node` with both roles and `allowedOutbounds: ["dual-role-node"]` will only route traffic to its own outbound, not to any other same-region outbound.

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/sing-box-operator-2:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/sing-box-operator-2:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/sing-box-operator-2:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/sing-box-operator-2/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

### Client DNS routing

The built-in template and `config/samples/client-template-configmap.yaml` use Cloudflare DoH (`https://1.1.1.1/dns-query`) for domains not explicitly matched by a local DNS rule, including `google.com`. Only `geosite-cn` (and explicit private-domain rules in the sample) uses local DNS. There is no automatic local-DNS fallback on DoH failure.

The `proxy-dns` tag opts a template into managed DNS wiring:

- `proxy-dns` is the **Auto/fallback** resolver; its `detour` uses the first available regional selector (`hk`, then `jp`, then `us`). For each available region, the operator also creates `proxy-dns-hk`, `proxy-dns-jp`, or `proxy-dns-us` with that selector as its detour. [DNS `clash_mode` rules](https://sing-box.sagernet.org/configuration/dns/rule/) select the matching resolver in `HK`/`JP`/`US` mode. Thus `proxy-dns.detour: hk` alone no longer means all DNS is pinned to HK. Selecting a node within a regional selector also controls that region's DNS connections. Unavailable regional modes are not generated.
- Explicit template DNS rules run **before** mode defaults, so domestic/private exceptions remain local in every mode. The `proxy-dns-{hk,jp,us}` DNS tags are reserved; a conflicting template tag causes a clear generation error rather than a duplicate tag.
- Normal domain destinations are resolved through DNS routing (`action: resolve`, without a `server` override) after leading sniff/DNS interception and explicit AI policy rules, but before ordinary business routing. This makes HTTP/SOCKS domain requests and direct destinations obey the DNS mode too, instead of bypassing it through a fixed dial-field resolver. IP destinations are not re-resolved. Explicit AI policy routes retain their existing resolution behavior.
- `dns.final` and the infrastructure dial-field `route.default_domain_resolver` remain `proxy-dns` as the Auto fallback. Normal destination lookups use the mode-aware resolve step above; internal lookups such as direct rule-set downloads may still use the default region. Unlike legacy DNS servers, [modern HTTPS DNS servers](https://sing-box.sagernet.org/configuration/dns/server/https/) dial directly without an explicit detour; `route.final` does not control their exit.
- Generated proxy endpoint **hostnames** receive `domain_resolver: local` unless an explicit resolver already exists, avoiding a proxy/DNS bootstrap cycle. IP endpoints require no bootstrap DNS. Prefer IP endpoints if bootstrap hostname pollution is a concern. These [dial-field resolvers](https://sing-box.sagernet.org/configuration/shared/dial/#domain_resolver) resolve the proxy server address, not the destination website.
- When no regional group exists, DoH connects **directly over TLS** as an availability fallback. It never switches to the system resolver, but the local network can still block that DoH connection. A configured but unreachable proxy does not trigger this fallback.

Existing ConfigMap templates with `proxy-dns` receive the same wiring when subscriptions are regenerated. Custom DNS rules are preserved and may deliberately override the default; audit any broad rules pointing to `local`. Templates without `proxy-dns` are left unchanged. The sample no longer sends a hard-coded Chinese EDNS client subnet to every resolver.

Regional DNS transports have separate caches in sing-box 1.14+. Mode switching has been integration-tested with sing-box 1.14.4 against local DoH/proxy fixtures, including repeated queries for the same domain and direct HTTP destination resolution. OS/browser caches and existing application connections can still outlive a switch; switching nodes within one region can also reuse that region's DNS cache. To run the opt-in integration test:

```sh
SING_BOX_BINARY=/path/to/sing-box go test ./internal/apiserver -run TestClientDNSRuntime -v
```

After upgrading the operator, refresh the client subscription, reload sing-box, and clear OS/browser DNS caches. Only DNS handled by sing-box is covered: applications using their own DoH or DNS traffic bypassing TUN/TProxy need separate configuration. These settings do not change server-side DNS resolution.

### Client policy routing

Client configurations normalize ordinary targets into `hk`, `jp`, and `us` selectors and aggregate routed `EgressPolicy` paths in the `ai` selector. There is no `proxy` or per-policy selector. Clash API exposes exactly `Auto`, `HK`, `JP`, and `US`; `AI` is an outbound selector, never a Clash mode. `Auto` defaults to `hk` when no future automatic region rule matches. Policy targets are excluded from regional groups; unavailable paths reject matching traffic rather than falling back. AI rules are emitted before domestic direct rules and Clash Mode rules, after the template's leading sniff/DNS preprocessing; existing custom rule order is otherwise preserved. A policy `ruleSet` is accepted only when the referenced tag is already defined by the template's `route.rule_set`.

This feature does not change server/DNS behavior. A TUN client may sniff a domain while the actual connection remains IP-addressed; the server's policy `fallbackAction: reject` can therefore reject it. Client policy routing must not be interpreted as end-to-end domain preservation.
