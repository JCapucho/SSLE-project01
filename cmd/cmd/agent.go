package cmd

import (
	"github.com/spf13/cobra"
)

// agentCmd represents the agent command
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage SSLE registry agents",
}

func init() {
	rootCmd.AddCommand(agentCmd)
}
