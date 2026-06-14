# Decisions

## [2026-05-01] Session Start

### Pre-existing LSP errors (NOT our concern)
- `internal/credmanager/credmanager_test.go`: wrong module path `github.com/your-org/singbox-operator`
- `internal/controller/proxyroute_controller_test.go`: same wrong module path
- `internal/webhook/proxynode_webhook.go`: same wrong module path
- `charts/singbox-operator/templates/*.yaml`: Helm template YAML errors (not Go)
These are pre-existing and unrelated to our task.

### DeriveUUID Export Decision
- Must export as `DeriveUUID` (capital D) for direct testing in `engine_test.go`
- Internal callers use `DeriveUUID` directly

### Test Updates Required
- Test 4 (`TestConfigEngine_ManualRoute`): Change expected inbound tag from `inbound-vless-node-b` to `inbound-vless`, user name from `user-dave` to `user-dave#node-b`
- Test 11 (`TestConfigEngine_MultiRouteInbounds`): Change from 2 inbounds to 1 inbound, 2 virtual users, no port offset check
- Test 12 (`TestDeriveUUID`): New test for UUID determinism
