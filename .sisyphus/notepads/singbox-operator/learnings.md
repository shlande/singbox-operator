# Learnings — singbox-operator

## Project Overview
- Kubernetes Operator for sing-box proxy configuration orchestration
- Three-layer CRD: ProxyNode / ProxyUser / ProxyRoute
- ConfigEngine computes complete sing-box config.json per node
- Module: github.com/shlande/singbox-operator
- Domain: proxy.io
- K8s 1.28+, namespace-scoped

## Key Architecture Decisions
- ProxyNode.spec.address = public IP (NOT ClusterIP)
- Two NodePort types: entry (per-protocol) + relay (relayPort, SOCKS5)
- ProxyUser → ProxyNode association via protocol matching (NOT nodeRef)
- ConfigEngine is pure function (no K8s API calls)
- Node credentials stored in K8s Secret (OwnerReference → ProxyNode)
- No relay role — only inbound/outbound (can be both)

## Protocols Supported
- vless (default port 10443)
- trojan (default port 10444)
- socks5 (default port 10808)
- http (default port 10080)

## Default Values (injected by MutatingWebhook)
- relayPort: 10808
- relayProtocol: "socks5"
- resources: requests cpu=100m,mem=128Mi; limits cpu=1,mem=512Mi

## [Task 1 Complete] CRD Types
- kubebuilder version used: v4.14.0
- go module: github.com/shlande/singbox-operator
- CRD files location: config/crd/bases/
- DeepCopy generated at: api/v1alpha1/zz_generated.deepcopy.go
- All 3 CRDs registered in SchemeBuilder
- Test file: api/v1alpha1/types_test.go
- go version: 1.26.2 darwin/arm64
- ProxyUser imports corev1 for SecretReference
- `make generate` regenerates zz_generated.deepcopy.go after type changes
- `make manifests` regenerates CRD YAMLs from +kubebuilder markers
- All 4 unit tests pass: TestProxyNodeDeepCopy, TestProxyUserDeepCopy, TestProxyRouteDeepCopy, TestProxyNodeStatusConditionsInit

## [Task 2 Complete] ConfigEngine
- Input struct fields: Node, Users, UserCreds, OutboundNodes, Routes, NodeCreds, OutboundNodesByName
- Output struct: Config []byte, Hash string
- sing-box JSON structure: log + inbounds + outbounds + route
- inbound role: user inbounds + socks5 outbounds to outbound nodes + direct
- outbound role: relay socks5 inbound + direct outbound
- multi-role: merged inbounds/outbounds
- ComputeHash: sha256[:8] hex = 16 chars
- Test file: internal/configengine/engine_test.go (10 cases, 91.9% coverage)
- ExtractNodePorts: returns SupportedProtocols ports + RelayPort for outbound nodes
- buildUserInbounds handles: vless (uuid), trojan (password), socks5 (username+password), http (uuid+password)
- deduplicateByTag prevents duplicate outbound entries in multi-role scenarios
- inbounds field initialized to []interface{}{} (never nil) even when empty

## [Task 3 Complete] CredManager + Test Infrastructure
- Secret name pattern: proxynode-{nodeName}-relay-cred
- Secret data keys: username, password
- EnsureNodeCredential: idempotent, sets OwnerReference for GC
- GetUserCredential: reads from user.spec.authSecret reference
- envtest setup: uses config/crd/bases/ for CRD loading; getFirstFoundEnvTestBinaryDir() scans bin/k8s/ for installed envtest binaries
- Test suite: internal/controller/suite_test.go (Ginkgo) — already existed, no modification needed
- Test helpers: test/helpers/helpers.go
- envtest binaries path: bin/k8s/1.35.0-darwin-arm64/ (installed via make setup-envtest)
- KUBEBUILDER_ASSETS must point to the versioned dir (e.g. bin/k8s/1.35.0-darwin-arm64), not bin/k8s/
- go build ./... passes cleanly with these new packages

## [Task 4 Complete] ProxyNode Reconciler
- Finalizer: proxy.io/proxynode-finalizer
- ConfigMap name: {nodeName}-config
- Deployment name: {nodeName}-deploy
- Relay Service name: {nodeName}-relay-svc
- Entry Service name: {nodeName}-{protocol}-entry-svc
- configHashAnnotation: proxy.io/config-hash
- singboxImage: ghcr.io/sagernet/sing-box:latest
- collectInput() pre-fetches all data before calling ConfigEngine
- sameRegionNodeMapper: triggers same-region nodes on ProxyNode change
- matchingProtocolNodeMapper: triggers inbound nodes on ProxyUser change
- affectedByRouteMapper: triggers inbound node on ProxyRoute change
- meta.SetStatusCondition import: k8s.io/apimachinery/pkg/api/meta (aliased as apimeta to avoid collision with k8s.io/apimachinery/pkg/api/errors)
- suite_test.go uses package controller (not controller_test) — tests must be in same package
- suite_test.go exposes k8sClient (not testClient) as the envtest client
- credmanager.EnsureNodeCredential returns (NodeCredential, error) — not just error
- Two reconcile passes needed in tests: first adds finalizer (Requeue:true), second does actual work
- Pre-existing scaffold tests for ProxyRoute/ProxyUser have invalid empty specs — pre-existing failures, not regression

## [Task 5 Complete] ProxyUser Reconciler
- Core responsibility: trigger ProxyNode reconcile via annotation update
- triggerNodeReconcile: updates proxy.io/reconcile-trigger annotation on ProxyNode
- findMatchingInboundNodes: lists all inbound ProxyNodes supporting user's protocol
- Status: ActiveNodeCount, ActiveNodes, ObservedGeneration, Ready condition
- Tests: 4 cases (matching nodes, multiple nodes, no match, deleted resource)

## [Task 6 Complete] ProxyRoute Reconciler
- Core responsibility: validate nodes exist, trigger inbound node reconcile
- setDegradedRoute: sets Degraded condition with reason and message
- updateRouteStatus: sets ResolvedInboundNode, ResolvedOutboundNode, Ready condition
- Tests: 4 cases (resolved route, degraded inbound, degraded outbound, deleted route)
- Test isolation: use dedicated namespace (pr-test-<GinkgoParallelProcess()>) to avoid polluting default namespace
- Pre-existing flakiness: ProxyUser "should update status with matching inbound nodes" fails with some seeds due to ProxyNode tests leaking vless inbound nodes into default namespace — not caused by ProxyRoute tests
- All ProxyRoute tests always pass in isolation (--ginkgo.focus="ProxyRoute")

## [Task 7 Complete] Webhooks
- ProxyNodeWebhook: implements both Defaulter[*v1alpha1.ProxyNode] and Validator[*v1alpha1.ProxyNode]
- Default: relayPort=10808, relayProtocol=socks5, protocol default ports (vless=10443, trojan=10444, socks5=10808, http=10080)
- Validate: address non-empty, relayPort range (1024-65535), no duplicate protocols, no port conflicts, roles valid (inbound/outbound only)
- ProxyUserWebhook: Validator only (protocol non-empty + known, authSecret.name non-empty)
- ProxyRouteWebhook: Validator only (inboundNode/outboundNode non-empty)
- Tests: pure unit tests, no envtest needed, 30 tests all pass
- Webhook registration: SetupXxxWebhookWithManager() in cmd/main.go using proxywebhook alias (avoids collision with ctrl-runtime webhook import)
- controller-runtime v0.23.3 uses generic APIs: ctrl.NewWebhookManagedBy(mgr, &T{}).WithDefaulter(...).WithValidator(...)
- Interfaces: admission.Defaulter[T] and admission.Validator[T] (not CustomDefaulter/CustomValidator which are deprecated)

## [Task 8 Complete] Prometheus Metrics
- 5 metrics: singbox_proxy_nodes_total, singbox_proxy_users_total, singbox_reconcile_duration_seconds, singbox_reconcile_errors_total, singbox_config_updates_total
- Registration: metrics.Registry.MustRegister() in init() in internal/metrics/metrics.go
- Instrumentation: reconcile duration in all 3 reconcilers, node/user counts in status updates
- /metrics endpoint: already provided by controller-runtime at :8080/metrics
- ConfigUpdatesTotal incremented when config hash changes (reconcileConfigMap) or config is updated (OperationResultUpdated)
- All metrics use low-cardinality labels only (region, role, phase, protocol, controller, result, error_type, node_region, trigger)

## [Task 9 Complete] Helm Chart
- Chart location: charts/singbox-operator/
- CRDs in crds/ (not templates/) to prevent helm upgrade deletion
- Templates: deployment, serviceaccount, role, clusterrole, rolebinding, clusterrolebinding, webhook configs, webhook service, cert-manager certificate
- Examples: 4 files with full comments (proxynode-inbound, proxynode-outbound, proxyuser, proxyroute)
- helm lint: passes with no ERRORs (only INFO about missing icon)
- LSP errors in .yaml template files are false positives — YAML LSP doesn't understand Go template syntax `{{ }}`, but helm lint/template verify correctness
