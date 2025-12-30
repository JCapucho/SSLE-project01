package cmd

import (
	"github.com/spf13/cobra"
)

// nodeCmd represents the agent command
var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage SSLE registry nodes",
}

func init() {
	rootCmd.AddCommand(nodeCmd)
}
