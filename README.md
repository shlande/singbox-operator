# sing-box-operator-2

A Kubernetes operator for managing [sing-box](https://github.com/SagerNet/sing-box) proxy nodes as Kubernetes custom resources. The operator deploys, configures, and connects proxy nodes with inbound (client-facing protocols) and outbound (upstream exit) roles across regions.

## Description

The sing-box-operator-2 manages a fleet of sing-box proxy nodes via the `SingBoxNode` custom resource. Each node can act as an **inbound** (accepting client connections via hysteria2, vless, trojan, etc.), an **outbound** (forwarding traffic upstream), or both. The operator generates sing-box configuration files automatically, handles TLS certificates, and supports explicit routing between nodes using `CustomRoute` resources. A key access-control feature is **AllowedInbounds**, which restricts which inbound nodes may use a given outbound node — empty means allow all (backward compatible), and when set, routing is gated on top of any explicit `CustomRoute` bindings.

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

### CustomRoute interaction

When a `CustomRoute` resource explicitly binds inbound node B to outbound node A, the binding is still gated by `A.Spec.AllowedInbounds`. If `A.AllowedInbounds` does not include B's name, the CustomRoute is skipped. The check is an AND gate: both the CustomRoute must exist AND `AllowedInbounds` must permit the binding (or be empty).

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
