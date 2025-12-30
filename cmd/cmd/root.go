package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"ssle/services"
)

var (
	CAFile         string
	CrtFile        string
	KeyFile        string
	ClusterAddress string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ssle-cli",
	Short: "Helper client for managing the SSLE service registry",
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&CAFile, "ca", "ca.crt", "Path to the certificate authority file")
	rootCmd.PersistentFlags().StringVar(&CrtFile, "crt", "peer.crt", "Path to a peer certificate file")
	rootCmd.PersistentFlags().StringVar(&KeyFile, "key", "peer.key", "Path to a peer key file")
	rootCmd.PersistentFlags().StringVar(&ClusterAddress, "cluster", "127.0.0.1:2382", "Address of the cluster peer api")
}

func NewPeerApiClient() services.PeerAPIClient {
	CAPem, err := os.ReadFile(CAFile)
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(CAPem)

	cert, err := tls.LoadX509KeyPair(CrtFile, KeyFile)
	if err != nil {
		log.Fatalf("Failed to load peer credentials: %v", err)
	}

	transportCred := credentials.NewTLS(&tls.Config{
		ServerName:   "registry.cluster.internal",
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{cert},
	})

	conn, err := grpc.NewClient(ClusterAddress, grpc.WithTransportCredentials(transportCred))
	if err != nil {
		log.Fatalf("Failed to create grpc client: %v", err)
	}
	return services.NewPeerAPIClient(conn)
}
