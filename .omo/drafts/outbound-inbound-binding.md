---
slug: outbound-inbound-binding
status: awaiting-approval
intent: clear
pending-action: write .omo/plans/outbound-inbound-binding.md
approach: Add allowedInbounds []string to SingBoxNodeSpec (outbound side, reverse declaration). Empty = allow all (backward compatible). Same-region auto-pairing preserved; binding is a deny-win tightening filter applied in collectInput and configengine. allowlist-only. CustomRoute stays additive; binding is independent AND gate.
---

# Draft: outbound-inbound-binding

## Components (topology ledger)
- C1 | outbound-binding API surface (CRD field + markers + deepcopy) | active | api/v1alpha1/singboxnode_types.go
- C2 | configengine applies binding as filter | active | internal/configengine/engine.go, internal/controller/singboxnode_controller.go
- C3 | webhook validates allowedInbounds names | active | internal/webhook/singboxnode_webhook.go
- C4 | backward-compat default (empty = allow all) verified | active | internal/configengine/engine_test.go, internal/controller/singboxnode_controller_test.go

## Open assumptions (announced defaults)
<!-- Intent is CLEAR: research resolves ambiguity, defaults are adopted (not asked), and each is surfaced in the plan's human TL;DR for veto. -->
- allowedInbounds semantics = empty means allow all (backward compatible) | adopted | user confirmed fork 2 | reversible
- allowlist only, no deniedInbounds | adopted | user confirmed fork 3 | reversible
- CustomRoute stays additive; binding is separate AND gate | adopted | user confirmed fork 4 | reversible
- Binding declared on outbound (A) side as allowedInbounds | adopted | user confirmed fork 1 | reversible
- Test strategy: TDD with Ginkgo/envtest | adopted | standard project convention | reversible

## Findings (cited - path:lines)
- api/v1alpha1/singboxnode_types.go:54-91 - SingBoxNodeSpec has no inbound/outbound binding field; Roles []ProxyRole (inbound|outbound).
- internal/controller/singboxnode_controller.go:213-224 - collectInput auto-collects same-region outbound nodes into input.OutboundNodes with no node-level filtering.
- api/v1alpha1/customroute_types.go:24-31 - CustomRoute only supports (inboundNode, outboundNode) one-way route, additive semantics, non-exclusive.
- internal/configengine/engine.go:234-245 - routesForNode only checks route.Spec.InboundNode == current node name.
- internal/configengine/engine.go:304-375 - buildRouteInbounds: same-region auto-pairing and CustomRoute coexist in config generation (buildOutboundNodeOutbounds + buildRouteOutbounds).
- internal/configengine/engine.go:445-506 - buildOutboundNodeOutbounds and buildRouteOutbounds iterate input.OutboundNodes without any binding filter.
- api/v1alpha1/usergroup_types.go:25-37 - AllowedNodes/DeniedNodes is user→node dimension, not node→node.
- internal/controller/singboxnode_controller.go:262-282 - UserGroup filtering is per-user in collectInput; cannot express node-pair binding.
- internal/webhook/singboxnode_webhook.go:53-118 - no node-pair binding validation.
- internal/controller/customroute_controller.go:47-101 - no exclusivity check; CustomRoute triggers inbound reconcile but does not affect same-region auto-pairing.
- internal/configengine/engine.go:188-205 - IsNodeAllowed helper with deny-win semantics; can be reused for binding check.

## Decisions (with rationale)
1. Add `AllowedInbounds []string` to SingBoxNodeSpec (outbound side). Rationale: user confirmed fork 1; reverse declaration on A gives A's owner full visibility of who can use it; single source of truth.
2. Empty AllowedInbounds = allow all (backward compatible). Rationale: user confirmed fork 2; preserves existing same-region auto-pairing behavior for all存量 nodes.
3. allowlist-only (no deniedInbounds). Rationale: user confirmed fork 3; matches "only allow specific B" requirement; simpler webhook validation.
4. CustomRoute stays additive; binding is independent AND gate. Rationale: user confirmed fork 4; no breaking change to CustomRoute semantics; binding filter applies on top of both auto-pairing and CustomRoute-derived outbound candidates.
5. Apply binding filter in collectInput (controller) AND in configengine (defensive). Rationale: collectInput is the natural place to filter OutboundNodes; configengine already has IsNodeAllowed pattern as defensive guard (see UserNodeRestrictions handling at engine.go:335, 361, 382, 486).
6. When outbound node A declares AllowedInbounds=[B1,B2], only B1 and B2 may use A as outbound. Other inbound nodes in same region will NOT have A in their OutboundNodes. Rationale: the filter is applied from B's perspective when B collects its OutboundNodes - B checks each candidate outbound A's AllowedInbounds to see if B is in it.

## Scope IN
- Add AllowedInbounds []string field to SingBoxNodeSpec with kubebuilder markers (+optional, +listType=set)
- Regenerate CRDs/deepcopy via make manifests generate
- Apply binding filter in collectInput: when B (inbound) collects OutboundNodes, skip outbound node A if A.Spec.AllowedInbounds is non-empty AND B.Name not in A.Spec.AllowedInbounds
- Apply defensive filter in configengine buildOutboundNodeOutbounds and buildRouteOutbounds
- Webhook validation: AllowedInbounds entries must be non-empty strings (MinLength=1); warn (not error) if a listed inbound name does not currently exist (since nodes can be created in any order)
- Tests: TDD with Ginkgo covering (a) empty AllowedInbounds = backward compat, (b) non-empty AllowedInbounds restricts, (c) cross-region unchanged, (d) CustomRoute still works alongside binding, (e) self-outbound (inbound+outbound same node) unaffected

## Scope OUT (Must NOT have)
- Do NOT add deniedInbounds field (allowlist-only per fork 3)
- Do NOT change CustomRoute semantics (stays additive per fork 4)
- Do NOT remove or alter same-region auto-pairing (preserved per fork 2)
- Do NOT change UserGroup allow/deny semantics (orthogonal, user-level)
- Do NOT introduce new CRD (no NodePair per fork 1)
- Do NOT add binding field to inbound side (declared on outbound only per fork 1)
- Do NOT make AllowedInbounds required (must stay optional for backward compat)

## Open questions
None - all forks resolved by user.

## Approval gate
status: approved (user confirmed all 4 forks 2026-07-05)
Metis gap analysis completed (ses_0d1e4666fffeWxId115lZsHJFb). Findings folded into plan:
- CRITICAL Finding 3 (cross-region reconcile trigger) -> task 2 now adds customRouteOutboundNodeMapper
- MAJOR Finding 1/2/8 (buildExperimentalConfig/buildRouteInbounds/routedNodes docs) -> task 3 now documents why no defensive filter needed + adds comments
- MAJOR Finding 5 (self-outbound test gap) -> task 5 adds scenario (e2)
- MAJOR Finding 6 (CustomRoute semantic change) -> Must NOT section now explicitly acknowledges
Plan file written: .omo/plans/outbound-inbound-binding.md
