# Learnings

- Constants in `api/v1alpha1/singboxnode_types.go` — added `NodeReadyConditionType` and `OfflineAnnotation` after the ProxyRole block.
- Pattern: exported string constants with doc comments, matching Go conventions in this file.
- Verified that `go build ./...` and `make test` both pass after adding constants.
- Constants don't need `make generate` or `make manifests` since they aren't CRD schema.