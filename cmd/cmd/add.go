package cmd

import (
	"context"
	"fmt"
	"ssle/services"

	"github.com/spf13/cobra"
)

func init() {
	var (
		location   string
		datacenter string
		nodeCrt    string
		nodeKey    string

		isObserver bool
	)

	// addCmd represents the add command
	var addCmd = &cobra.Command{
		Use:   "add",
		Short: "Add an node to the SSLE registry",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var node_type services.NodeType
			if isObserver {
				node_type = services.NodeType_OBSERVER
			} else {
				node_type = services.NodeType_AGENT
			}

			peer_api_client := NewPeerApiClient()
			res, err := peer_api_client.AddNode(context.Background(), &services.AddNodeRequest{
				Name:       &args[0],
				Location:   &location,
				Datacenter: &datacenter,

				NodeType: &node_type,
			})

			if err != nil {
				fmt.Printf("Failed to add node: %v\n", err)
			} else {
				err = writeToFile(nodeCrt, res.Certificate)
				if err != nil {
					panic(err.Error())
				}
				err = writeToFile(nodeKey, res.Key)
				if err != nil {
					panic(err.Error())
				}
			}
		},
	}

	nodeCmd.AddCommand(addCmd)

	addCmd.Flags().BoolVar(&isObserver, "observer", false, "Whether this node is a datacenter observer")
	addCmd.Flags().StringVar(&nodeCrt, "node-crt", "node.crt", "Path to where the node certificate will be written")
	addCmd.Flags().StringVar(&nodeKey, "node-key", "node.key", "Path to where the node key will be written")

	addCmd.Flags().StringVar(&location, "location", "", "Datacenter where the node is located")
	addCmd.Flags().StringVar(&datacenter, "datacenter", "", "The datacenter location")
	if err := addCmd.MarkFlagRequired("location"); err != nil {
		panic(err)
	}
	if err := addCmd.MarkFlagRequired("datacenter"); err != nil {
		panic(err)
	}
}
