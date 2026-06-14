# Decisions — singbox-operator

## Architectural Decisions
- ConfigEngine: pure function, no K8s API access
- ProxyUser Reconciler: triggers ProxyNode reconcile (does NOT modify ConfigMap directly)
- ProxyRoute Reconciler: triggers inboundNode reconcile (does NOT modify ConfigMap directly)
- Node-to-node auth: SOCKS5 credentials auto-generated, stored in Secret with OwnerReference
- Multi-role nodes: inbounds/outbounds merged, tags deduplicated

## Forbidden Patterns
- NO ClusterIP for outbound server addresses
- NO TUN/TPROXY (no NET_ADMIN/SYS_MODULE)
- NO hardcoded user config in ProxyNode spec
- NO ProxyUser nodeRef (protocol matching only)
- NO plaintext credentials in ConfigMap
- NO over-abstraction in ConfigEngine
- NO AI slop (no meaningless comments, empty interfaces, over-wrapping)
