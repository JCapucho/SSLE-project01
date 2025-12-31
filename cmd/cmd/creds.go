package cmd

import (
	"context"
	"fmt"
	"os"
	"ssle/services"

	"github.com/spf13/cobra"
)

func writeToFile(path string, content []byte) error {
	if path == "-" {
		_, err := os.Stdout.Write(content)
		println()
		return err
	} else {
		return os.WriteFile(path, content, 0600)
	}
}

func init() {
	var (
		datacenter string
		nodeCrt    string
		nodeKey    string
	)

	// credsCmd represents the creds command
	var credsCmd = &cobra.Command{
		Use:   "creds",
		Short: "Retrieve new node credentials",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			peer_api_client := NewPeerApiClient()
			res, err := peer_api_client.GetNodeCredentials(context.Background(), &services.GetNodeCredentialsRequest{
				Name:       &args[0],
				Datacenter: &datacenter,
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

	nodeCmd.AddCommand(credsCmd)

	credsCmd.Flags().StringVar(&nodeCrt, "node-crt", "node.crt", "Path to where the node certificate will be written")
	credsCmd.Flags().StringVar(&nodeKey, "node-key", "node.key", "Path to where the node key will be written")

	credsCmd.Flags().StringVar(&datacenter, "datacenter", "", "The datacenter location")
	if err := credsCmd.MarkFlagRequired("datacenter"); err != nil {
		panic(err)
	}
}
