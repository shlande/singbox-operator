# Issues — singbox-operator

## Known Gotchas (from Metis Review)
- Node Secret lifecycle: ProxyNode Finalizer must clean up node-level Secrets on deletion
- Cascade updates when outbound node changes: ProxyNode Controller must Watch same-region nodes
- relayPort conflict: MutatingWebhook detects port conflicts, ValidatingWebhook rejects duplicates
- Multi-role node config merge: ConfigEngine stacks inbounds/outbounds by role, same node can be inbound+outbound

## Open Issues
(none yet)
