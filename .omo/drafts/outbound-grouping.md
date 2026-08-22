---
slug: outbound-grouping
status: approved
intent: clear
pending-action: write .omo/plans/outbound-grouping.md
approach: Add optional spec.tag to SingBoxNode (inbound-side, single value, value != "default"); group client-config outbounds by inbound tag in apiserver BuildClientConfig; emit one selector per distinct tag + top-level selector("proxy") over groups + direct; TDD.
---

# Draft: outbound-grouping

## Components (topology ledger)
| id | outcome | status | evidence |
| --- | --- | --- | --- |
| C1 CRD field | SingBoxNodeSpec.Tag optional string, validated != "default" | active | api/v1alpha1/singboxnode_types.go:54-106 |
| C2 webhook | reject tag=="default" in singboxnode_webhook | active | internal/webhook/singboxnode_webhook.go:53-192 |
| C3 client config grouping | BuildClientConfig partitions outbounds by inbound tag, emits per-group selectors + top-level proxy selector | active | internal/apiserver/client_config.go:37-86 |
| C4 tests | TDD update + multi-group cases | active | internal/apiserver/handler_test.go, client_config_filter_test.go |

## Open assumptions (announced defaults)
| assumption | adopted default | rationale | reversible? |
| --- | --- | --- | --- |
| default group name for untagged inbounds | "default" | user F1 | yes |
| keep top-level selector("proxy") | yes | user F2; template route.final="proxy" + clash_mode rule needs it (template.go:46-47,68) | yes |
| group selector tag = raw inbound tag value | yes | user F3 | yes |
| tag is single value | yes | user F4 | yes |
| test strategy | TDD | user F5 | yes |
| tag=="default" forbidden | webhook rejects | user F6 | yes |
| tag pattern | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, MaxLength 63 | sing-box selector tag must be DNS-label-safe; matches existing naming conventions | yes |

## Findings (cited - path:lines)
- internal/apiserver/client_config.go:37-86 — BuildClientConfig: single selector("proxy") over all proxyTags
- internal/apiserver/client_config.go:60-69 — outbound tag = outboundName#inboundName (encodes inbound source)
- internal/apiserver/client_config.go:115-161 — resolveOutboundNodes per inbound (region + CustomRoute + AllowedInbounds/Outbounds + offline + UserGroup)
- internal/apiserver/template.go:41-71 — DefaultTemplate route.final="proxy", clash_mode Proxy rule → proxy must remain
- internal/apiserver/template.go:74-83 — MergeOutbounds replaces outbounds array wholesale
- internal/apiserver/handler.go:149,173 — BuildClientConfig → MergeOutbounds pipeline
- internal/webhook/singboxnode_webhook.go:53-192 — validateSingBoxNode pattern (field.ErrorList)
- internal/webhook/singboxnode_webhook.go:213-222 — SetupSingBoxNodeWebhookWithManager registration
- api/v1alpha1/singboxnode_types.go:54-106 — SingBoxNodeSpec, no tag field today
- internal/apiserver/handler_test.go:28-73 — TestBuildClientConfig_TwoOutboundNodes asserts len==4, selector.outbounds has 2 tags
- internal/apiserver/client_config_filter_test.go:93,247,320 — assert exact result lengths (will change)
- internal/configengine/engine.go — node-side config, NOT touched (confirmed no selector/group concept there)

## Decisions (with rationale)
- D1: tag validation in webhook not CRD marker — webhook already centralizes SingBoxNode validation (singboxnode_webhook.go:53); keeps "!=default" logic with other semantic checks. CRD marker adds MaxLength + Pattern for syntactic gate.
- D2: group order = sorted by tag string for deterministic output (config hash stability). Matches existing sort discipline (client_config.go:157-159).
- D3: outbound entries NOT deduplicated across groups — tags differ via #inbound suffix so they're distinct sing-box outbounds; an outbound used by 2 inbounds with different tags appears in 2 groups (intended).
- D4: empty tag on inbound → group "default". Multiple untagged inbounds merge into single "default" group.
- D5: NO configengine changes — grouping is purely client-side. Node-side sing-box config unchanged.

## Scope IN
- Add Tag field to SingBoxNodeSpec + CRD markers + webhook validation (!= "default")
- Refactor BuildClientConfig to partition by inbound tag, emit per-group selectors + top-level proxy selector + direct
- Update existing apiserver tests (assertions change due to new selector layer)
- Add new tests: multi-group, default-group merge, tag=="default" rejected, tag pattern validation
- make manifests generate (CRD + deepcopy regeneration)

## Scope OUT (Must NOT have)
- NO changes to configengine (node-side config)
- NO multi-value tags (tags []string)
- NO changes to MergeOutbounds or DefaultTemplate
- NO changes to route rules / final outbound (proxy stays final)
- NO e2e test changes (unit-test scope only)
- NO grouping prefix on selector tags (use raw tag value, not "group-<tag>")

## Open questions
(none — all F1-F6 resolved)

## Approval gate
status: approved
User approved approach + all forks F1-F6 on this turn. Proceeding to write .omo/plans/outbound-grouping.md.

## Metis gap analysis receipt
- Session: ses_0cd08f0d3ffeEzSt1uIP3mJJv5 (Metis reviewer)
- Verdict: REVISE → fixes applied → re-approved
- Findings addressed:
  - F1 (BLOCKER): missing test assertion sites — added handler_test.go:232 (ExplicitRoutes), :927 (DualRoleNode_IncludesSelf), corrected client_config_filter_test.go:247 (3→4)
  - F2 (BLOCKER): incorrect len counts for 0-proxy cases — confirmed lines 93/320/754/785/806/900/182 need NO change (0 proxy → no group selector → still 2 items)
  - F3 (MAJOR): empty group edge case — added explicit rule: no selector emitted if group has 0 outbounds
  - F10 (MAJOR): self-as-outbound dedup — added sort+compact dedup within groups
  - F13 (MAJOR): T3 not independently testable — merged impl+2 tests into T3 (MultiGroup + EmptyGroupNotEmitted)
- All cited line numbers verified by independent read of source files.
