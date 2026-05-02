package user

import "github.com/spf13/cobra"

func NewUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage sing-box users",
	}
	cmd.AddCommand(NewInitCmd())
	return cmd
}
