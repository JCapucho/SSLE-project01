package state

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	dockerClient "github.com/docker/docker/client"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/resolver"

	"ssle/agent/config"
	pb "ssle/services"
)

const (
	registryResolverScheme = "registry"
)

func loadAgentCrt(config *config.Config) (string, string, tls.Certificate) {
	certFile := filepath.Join(config.Dir, "agent.crt")
	keyFile := filepath.Join(config.Dir, "agent.key")

	crtBytes, err := os.ReadFile(certFile)
	if err != nil && os.IsNotExist(err) {
		crtBytes, err = os.ReadFile(config.CrtFile)
	}
	if err != nil {
		log.Fatalf("Failed to load agent certificate: %v", err)
	}

	keyBytes, err := os.ReadFile(keyFile)
	if err != nil && os.IsNotExist(err) {
		keyBytes, err = os.ReadFile(config.KeyFile)
	}
	if err != nil {
		log.Fatalf("Failed to load agent key: %v", err)
	}

	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		log.Fatalf("Failed to load agent key pair: %v", err)
	}

	err = os.WriteFile(certFile, crtBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write agent certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write agent key: %v", err)
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

func loadRegistryAddresses(config *config.Config) (string, []string) {
	addrsFile := filepath.Join(config.Dir, "addrs")

	file, err := os.Open(addrsFile)
	if err != nil {
		if os.IsNotExist(err) {
			addrs := strings.Split(config.JoinUrl, ",")

			err := writeRegistryAddresses(addrsFile, addrs)
			if err != nil {
				log.Fatalf("Error: Failed to write registry addresses: %v", err)
			}

			return addrsFile, addrs
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

	return addrsFile, lines
}

type State struct {
	mu sync.Mutex

	credentials    *tls.Certificate
	RegistryClient pb.AgentAPIClient
	DockerClient   *dockerClient.Client

	SignatureVerifier *verify.Verifier
	SignatureIdentity *verify.CertificateIdentity

	resolver          *registryResolverBuilder
	addrsFile         string
	certFile, keyFile string

	eventsFile *os.File
}

func LoadState(config *config.Config) *State {
	opts := tuf.DefaultOptions()
	client, err := tuf.New(opts)
	if err != nil {
		panic(err)
	}

	trustedMaterial, err := root.GetTrustedRoot(client)
	if err != nil {
		panic(err)
	}

	verifier, err := verify.NewVerifier(
		trustedMaterial,
		verify.WithTransparencyLog(1),
		verify.WithIntegratedTimestamps(1),
	)
	if err != nil {
		panic(err)
	}

	certID, err := verify.NewShortCertificateIdentity(config.SigningIssuer, "", "", config.SigningSAN)
	if err != nil {
		panic(err)
	}

	err = os.Mkdir(config.Dir, 0700)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Failed to create state dir: %v", err)
	}

	CAPem, err := os.ReadFile(config.CAFile)
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}

	eventsFile, err := os.Create(config.EventsLog)
	if err != nil {
		log.Fatalf("Failed to open events log: %v", err)
	}

	certFile, keyFile, creds := loadAgentCrt(config)
	addrsFile, addrs := loadRegistryAddresses(config)
	resolver := &registryResolverBuilder{addrs: addrs}

	state := &State{
		credentials: &creds,
		addrsFile:   addrsFile,
		certFile:    certFile,
		keyFile:     keyFile,
		resolver:    resolver,

		SignatureVerifier: verifier,
		SignatureIdentity: &certID,

		eventsFile: eventsFile,
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
	state.RegistryClient = pb.NewAgentAPIClient(conn)

	dcli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}
	state.DockerClient = dcli

	return state
}

func (state *State) WriteEvent(event any) {
	msg, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to encode event: %v", err)
		return
	}
	msg = fmt.Appendf(msg, "\n")
	state.eventsFile.Write(msg)
}

func (state *State) UpdateCredentials(crtBytes []byte, keyBytes []byte) error {
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

func (state *State) clientCertificateForTLS(req *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return state.credentials, nil
}

func (state *State) UpdateAddrs(addrs []string) {
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
