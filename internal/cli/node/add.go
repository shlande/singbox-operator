package node

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

func NewAddCmd() *cobra.Command {
	var nodeRef string
	var address string
	var region string
	var roles []string
	var relayPort int32
	var namespace string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new SingBoxNode (inbound/outbound)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			validRoles := []string{string(proxyv1alpha1.ProxyRoleInbound), string(proxyv1alpha1.ProxyRoleOutbound)}
			parsedRoles := make([]proxyv1alpha1.ProxyRole, 0, len(roles))
			for _, r := range roles {
				if !slices.Contains(validRoles, r) {
					return fmt.Errorf("invalid role %q: must be one of inbound|outbound", r)
				}
				parsedRoles = append(parsedRoles, proxyv1alpha1.ProxyRole(r))
			}

			k8sClient, ok := cmd.Context().Value("k8s-client").(client.Client)
			if !ok {
				return fmt.Errorf("failed to get Kubernetes client from context")
			}

			node := &proxyv1alpha1.SingBoxNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: proxyv1alpha1.SingBoxNodeSpec{
					NodeRef:   nodeRef,
					Address:   address,
					Region:    region,
					Roles:     parsedRoles,
					RelayPort: relayPort,
				},
			}
			if err := k8sClient.Create(ctx, node); err != nil {
				return fmt.Errorf("creating SingBoxNode: %w", err)
			}

			fmt.Printf("SingBoxNode %q created in namespace %q\n  Roles: %v\n  NodeRef: %s\n  Address: %s\n  Region: %s\n",
				name, namespace, parsedRoles, nodeRef, address, region)
			return nil
		},
	}

	cmd.Flags().StringVar(&nodeRef, "node-ref", "", "Kubernetes Node name to run on (required)")
	cmd.Flags().StringVar(&address, "address", "", "Public IP or hostname of the host machine (required)")
	cmd.Flags().StringVar(&region, "region", "", "Geographic region label, e.g. hk (required)")
	cmd.Flags().StringSliceVar(&roles, "roles", []string{"inbound"}, "Proxy roles: inbound,outbound (comma-separated)")
	cmd.Flags().Int32Var(&relayPort, "relay-port", 0, "Host port for inter-node relay connections (outbound nodes)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")

	_ = cmd.MarkFlagRequired("node-ref")
	_ = cmd.MarkFlagRequired("address")
	_ = cmd.MarkFlagRequired("region")

	return cmd
}
