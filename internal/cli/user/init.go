package user

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

func NewInitCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Initialize a new User with a generated auth secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			k8sClient, ok := cmd.Context().Value("k8s-client").(client.Client)
			if !ok {
				return fmt.Errorf("failed to get Kubernetes client from context")
			}

			id := uuid.New().String()

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-auth",
					Namespace: namespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"uuid": []byte(id),
				},
			}
			if err := k8sClient.Create(ctx, secret); err != nil {
				return fmt.Errorf("creating auth secret: %w", err)
			}

			user := &proxyv1alpha1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: proxyv1alpha1.UserSpec{
					AuthSecret: corev1.SecretReference{
						Name:      name + "-auth",
						Namespace: namespace,
					},
				},
			}
			if err := k8sClient.Create(ctx, user); err != nil {
				_ = k8sClient.Delete(context.Background(), secret)
				return fmt.Errorf("creating user: %w", err)
			}

			fmt.Printf("User %q initialized in namespace %q\n  UUID: %s\n", name, namespace, id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")

	return cmd
}
