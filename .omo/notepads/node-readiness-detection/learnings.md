# Learnings

## 2026-06-07 Session Start

### Critical Scope Clarification (from user)
- **Client config 过滤基于 SingBoxNode.Status.Conditions[NodeReady]**，不是直接查 K8s Node
- Controller 负责 watch K8s Node → 更新 SingBoxNode.Status.Conditions[NodeReady]
- API server handleClientConfig 读取 SingBoxNode 的 NodeReady condition 来决定是否包含该节点
- offline annotation 打在 K8s Node 上，controller 读取后反映到 SingBoxNode status

### Architecture Flow
```
K8s Node (Ready/NotReady + annotation) 
  → Controller watches Node
  → Controller updates SingBoxNode.Status.Conditions[NodeReady=True/False]
  → API server reads SingBoxNode.Status.Conditions[NodeReady]
  → Client config 过滤
```

### Key Files
- `api/v1alpha1/singboxnode_types.go:27-31` — 常量定义模式
- `api/v1alpha1/singboxnode_types.go:96` — Conditions 字段 (+listType=map, +listMapKey=type)
- `internal/controller/singboxnode_controller.go:517-532` — SetupWithManager Watch 注册
- `internal/controller/singboxnode_controller.go:413-446` — updateStatus() 模式
- `internal/controller/singboxnode_controller.go:448-465` — setDegraded() 模式
- `internal/apiserver/client_config.go:14-20` — ClientConfigInput 结构体
- `internal/apiserver/client_config.go:97-130` — resolveOutboundNodes()
- `internal/apiserver/handler.go:77-108` — handleClientConfig()
- `cmd/main.go:196-205` — controller 注册位置

### Existing Patterns
- Condition 设置: `apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{...})`
- Mapper 函数签名: `func (r *SingBoxNodeReconciler) xxxMapper(ctx context.Context, obj client.Object) []reconcile.Request`
- RBAC for nodes 已声明 (singboxnode_controller.go:69): `// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch`
- Annotation key 域名: `singboxoperator.shlande.top/` (与 finalizer 一致)
- Finalizer: `singboxoperator.shlande.top/singboxnode-finalizer`

## 2026-06-07 Code Quality Review

### Automated Checks
- `go vet ./...` — clean
- `go build ./...` — clean
- `make lint-fix` — 38 pre-existing issues, none in the 4 reviewed files

### Specific Concerns Verified
1. **isSingBoxNodeReady() backward compatibility** (handler.go:162-170): Correct. No condition = returns true (ready). Only marks offline when explicitly False.
2. **updateNodeReadyStatus() IsNotFound handling** (singboxnode_controller.go:421-431): Correct. Generic `err != nil` catches all Get errors including IsNotFound. Sets ConditionFalse with reason "NodeNotFound".
3. **nil map access for OfflineNodeNames** (client_config.go:35): Safe. Go nil map read returns zero-value (false). `!false = true` → no filtering, correct uninitialized semantics.
4. **nil Annotations map** (node_health.go:23): Safe. `nil[key]` returns `""`. `"" == "true"` is false = not offline annotated.

### Verdict: APPROVE
No blocking issues. All edge cases handled correctly. Clean separation between concern layers (node_health, controller, handler, client_config).
