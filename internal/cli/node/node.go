package node

import "github.com/spf13/cobra"

func NewNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage SingBoxNode resources",
	}
	cmd.AddCommand(NewAddCmd())
	return cmd
}
