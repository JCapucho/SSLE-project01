package node_utils

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"ssle/services"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/resolver"
)

const (
	registryResolverScheme = "registry"
)

func loadNodeCrt(stateDir string, crtFilePath string, keyFilePath string) (string, string, tls.Certificate) {
	certFile := filepath.Join(stateDir, "node.crt")
	keyFile := filepath.Join(stateDir, "node.key")

	crtBytes, err := os.ReadFile(certFile)
	if err != nil && os.IsNotExist(err) {
		crtBytes, err = os.ReadFile(crtFilePath)
	}
	if err != nil {
		log.Fatalf("Failed to load node certificate: %v", err)
	}

	keyBytes, err := os.ReadFile(keyFile)
	if err != nil && os.IsNotExist(err) {
		keyBytes, err = os.ReadFile(keyFilePath)
	}
	if err != nil {
		log.Fatalf("Failed to load node key: %v", err)
	}

	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		log.Fatalf("Failed to load node key pair: %v", err)
	}

	err = os.WriteFile(certFile, crtBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write node certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write node key: %v", err)
	}

	return certFile, keyFile, keyPair
}

func writeRegistryAddresses(fileName string, addrs []string) error {
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

	w := bufio.NewWriter(file)
	for _, line := range addrs {
		fmt.Fprintln(w, line)
	}

	return w.Flush()
}

func loadRegistryAddresses(stateDir string, providedUrls []string) (string, []string) {
	addrsFile := filepath.Join(stateDir, "addrs")

	file, err := os.Open(addrsFile)
	if err != nil {
		if os.IsNotExist(err) {
			err := writeRegistryAddresses(addrsFile, providedUrls)
			if err != nil {
				log.Fatalf("Error: Failed to write registry addresses: %v", err)
			}

			return addrsFile, providedUrls
		} else {
			log.Fatalf("Failed to load registry addresses: %v", err)
		}
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if scanner.Err() != nil {
		log.Fatalf("Failed to load registry addresses: %v", err)
	}

	if len(lines) < 1 {
		err := writeRegistryAddresses(addrsFile, providedUrls)
		if err != nil {
			log.Fatalf("Error: Failed to write registry addresses: %v", err)
		}
		lines = providedUrls
	}

	return addrsFile, lines
}

type NodeState struct {
	mu sync.Mutex

	credentials       *tls.Certificate
	resolver          *registryResolverBuilder
	addrsFile         string
	certFile, keyFile string

	Connection *grpc.ClientConn
	NodeApi    services.NodeAPIClient
}

func LoadNodeState(
	stateDir string,
	caFile string,
	crtFile string,
	keyFile string,
	providedUrls []string,
) *NodeState {
	err := os.Mkdir(stateDir, 0700)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Failed to create state dir: %v", err)
	}

	CAPem, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}

	certFile, keyFile, creds := loadNodeCrt(stateDir, crtFile, keyFile)
	addrsFile, addrs := loadRegistryAddresses(stateDir, providedUrls)
	resolver := &registryResolverBuilder{addrs: addrs}

	state := &NodeState{
		credentials: &creds,
		addrsFile:   addrsFile,
		certFile:    certFile,
		keyFile:     keyFile,
		resolver:    resolver,
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(CAPem)

	transportCred := credentials.NewTLS(&tls.Config{
		ServerName:           "registry.cluster.internal",
		GetClientCertificate: state.clientCertificateForTLS,
		RootCAs:              caCertPool,
	})

	url := fmt.Sprintf("%v:///", registryResolverScheme)
	conn, err := grpc.NewClient(url, grpc.WithTransportCredentials(transportCred), grpc.WithResolvers(resolver))
	if err != nil {
		log.Fatalf("Failed to read grpc client: %v", err)
	}
	state.Connection = conn
	state.NodeApi = services.NewNodeAPIClient(conn)

	return state
}

func (state *NodeState) UpdateCredentials(crtBytes []byte, keyBytes []byte) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		return err
	}

	state.credentials = &keyPair

	err = os.WriteFile(state.certFile, crtBytes, 0600)
	if err != nil {
		log.Printf("Error: Failed to write agent certificate: %v", err)
	}

	err = os.WriteFile(state.keyFile, keyBytes, 0600)
	if err != nil {
		log.Printf("Error: Failed to write agent key: %v", err)
	}

	return nil
}

func (state *NodeState) clientCertificateForTLS(req *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return state.credentials, nil
}

func (state *NodeState) UpdateAddrs(addrs []string) {
	state.resolver.Update(addrs)

	err := writeRegistryAddresses(state.addrsFile, addrs)
	if err != nil {
		log.Printf("Error: Failed to write registry addresses: %v", err)
	}
}

type registryResolverBuilder struct {
	addrs    []string
	resolver *registryResolver
}

func (builder *registryResolverBuilder) Update(addrs []string) {
	builder.addrs = addrs
	builder.resolver.reload(builder.addrs)
}
func (builder *registryResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	builder.resolver = &registryResolver{
		target: target,
		cc:     cc,
	}
	builder.resolver.reload(builder.addrs)
	return builder.resolver, nil
}
func (*registryResolverBuilder) Scheme() string { return registryResolverScheme }

type registryResolver struct {
	target resolver.Target
	cc     resolver.ClientConn
}

func (r *registryResolver) reload(rawAddrs []string) {
	addrs := make([]resolver.Address, len(rawAddrs))
	for i, s := range rawAddrs {
		addrs[i] = resolver.Address{Addr: s}
	}
	r.cc.UpdateState(resolver.State{
		Addresses: addrs,
		Endpoints: []resolver.Endpoint{{Addresses: addrs}},
	})
}
func (*registryResolver) ResolveNow(resolver.ResolveNowOptions) {}
func (*registryResolver) Close()                                {}
