package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	cliuser "github.com/shlande/singbox-operator/internal/cli/user"
)

var (
	scheme     = runtime.NewScheme()
	kubeconfig string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxyv1alpha1.AddToScheme(scheme))
}

func main() {
	root := &cobra.Command{
		Use:   "sbctl",
		Short: "sbctl — sing-box operator CLI",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			kubeconfigPath := kubeconfig
			if kubeconfigPath == "" {
				kubeconfigPath = clientcmd.RecommendedHomeFile
			}
			cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}
			k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
			if err != nil {
				return fmt.Errorf("creating Kubernetes client: %w", err)
			}
			cmd.SetContext(context.WithValue(cmd.Context(), "k8s-client", k8sClient))
			return nil
		},
	}

	root.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")
	root.AddCommand(cliuser.NewUserCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
