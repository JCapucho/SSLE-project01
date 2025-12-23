package cmd

import (
	"context"
	"fmt"
	"os"
	"ssle/services"

	"github.com/spf13/cobra"
)

func init() {
	var (
		location   string
		datacenter string
		agentCrt   string
		agentKey   string
	)

	// addCmd represents the add command
	var addCmd = &cobra.Command{
		Use:   "add",
		Short: "Add an agent to the SSLE registry",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			peer_api_client := NewPeerApiClient()
			res, err := peer_api_client.AddAgent(context.Background(), &services.AddAgentRequest{
				Name:       &args[0],
				Location:   &location,
				Datacenter: &datacenter,
			})

			if err != nil {
				fmt.Printf("Failed to add agent: %v\n", err)
			} else {
				err = os.WriteFile(agentCrt, res.Certificate, 0600)
				if err != nil {
					panic(err.Error())
				}
				err = os.WriteFile(agentKey, res.Key, 0600)
				if err != nil {
					panic(err.Error())
				}

				println("Agent added successfully")
			}
		},
	}

	agentCmd.AddCommand(addCmd)

	addCmd.Flags().StringVar(&agentCrt, "agent-crt", "agent.crt", "Path to where the agent certificate will be written")
	addCmd.Flags().StringVar(&agentKey, "agent-key", "agent.key", "Path to where the agent key will be written")

	addCmd.Flags().StringVar(&location, "location", "", "Datacenter where the agent is located")
	addCmd.Flags().StringVar(&datacenter, "datacenter", "", "The datacenter location")
	if err := addCmd.MarkFlagRequired("location"); err != nil {
		panic(err)
	}
	if err := addCmd.MarkFlagRequired("datacenter"); err != nil {
		panic(err)
	}
}
