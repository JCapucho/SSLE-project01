package state

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"ssle/registry/config"
	"ssle/schemas"
)

type State struct {
	Token   []byte
	EtcdDir string

	CA             tls.Certificate
	CACrtFile      string
	AgentCA        tls.Certificate
	AgentCACrtFile string

	ServerKeyPair tls.Certificate
	ServerCrtFile string
	ServerKeyFile string
}

func createCA(token []byte, start time.Time, implicit string, ou string) ([]byte, []byte) {
	keyRandom, err := hkdf.Expand(sha256.New, token, implicit, ed25519.SeedSize)
	if err != nil {
		panic(err.Error())
	}

	pub, priv, err := ed25519.GenerateKey(bytes.NewReader(keyRandom))
	if err != nil {
		panic(err.Error())
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:       []string{"SSLE Project 01"},
			OrganizationalUnit: []string{ou},
		},
		NotBefore: start,
		// 10 Years
		NotAfter:              start.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, pub, priv)
	if err != nil {
		panic(err.Error())
	}

	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err.Error())
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDer})

	return crt, key
}

func addHostnameToCert(cert *x509.Certificate, hostname schemas.Hostname) {
	if hostname.IsAddress() {
		repr := hostname.Address().AsSlice()
		if !slices.ContainsFunc(cert.IPAddresses, func(a net.IP) bool { return a.Equal(repr) }) {
			cert.IPAddresses = append(cert.IPAddresses, repr)
		}
	} else {
		repr := hostname.Fqdn()
		if !slices.Contains(cert.DNSNames, repr) {
			cert.DNSNames = append(cert.DNSNames, repr)
		}
	}
}

func createNodeCrt(config config.Config, CA tls.Certificate) ([]byte, []byte) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err.Error())
	}

	notBefore := time.Now()
	// 90 Days
	notAfter := notBefore.Add(90 * 24 * time.Hour)

	template := x509.Certificate{
		Subject: pkix.Name{
			Organization:       []string{"SSLE Project 01"},
			OrganizationalUnit: []string{"Servers"},
			CommonName:         config.Name,
		},
		DNSNames:              []string{"registry.cluster.internal"},
		IPAddresses:           []net.IP{},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	addHostnameToCert(&template, config.PeerAdvertiseHostname)
	addHostnameToCert(&template, config.AgentAPIAdvertiseHostname)

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, CA.Leaf, pub, CA.PrivateKey)
	if err != nil {
		panic(err.Error())
	}

	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err.Error())
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDer})

	return crt, key
}

func decodeToken(encodedToken []byte) ([]byte, time.Time) {
	parts := bytes.SplitN(encodedToken, []byte("::"), 2)

	timeMilli, err := strconv.ParseInt(string(parts[0]), 10, 64)
	if err != nil {
		panic(err.Error())
	}

	rawToken := parts[1]

	token := make([]byte, base64.StdEncoding.DecodedLen(len(rawToken)))
	n, err := base64.StdEncoding.Decode(token, rawToken)
	if err != nil {
		log.Fatalf("Failed to decode token: %v", err)
	}
	return token[:n], time.UnixMilli(timeMilli)
}

func loadStateToken(config config.Config) ([]byte, time.Time) {
	tokenFile := filepath.Join(config.Dir, "token")

	var token []byte
	var start time.Time

	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		if config.InitialToken == "" {
			token = make([]byte, 32)
			rand.Read(token)
			start = time.Now()
		} else {
			token, start = decodeToken([]byte(config.InitialToken))
		}

		encodedToken := make([]byte, base64.StdEncoding.EncodedLen(len(token)))
		base64.StdEncoding.Encode(encodedToken, token)

		shareToken := slices.Concat(
			fmt.Appendf(nil, "%d", start.UnixMilli()),
			[]byte("::"),
			encodedToken,
		)

		os.WriteFile(tokenFile, shareToken, 0600)
	} else {
		encodedToken, err := os.ReadFile(tokenFile)
		if err != nil {
			log.Fatalf("Failed to read token file: %v", err)
		}
		token, start = decodeToken(encodedToken)
	}

	return token, start
}

func loadStateCA(config config.Config, token []byte, start time.Time) (string, string, tls.Certificate) {
	certFile := filepath.Join(config.Dir, "ca.crt")
	keyFile := filepath.Join(config.Dir, "ca.key")

	crtBytes, keyBytes := createCA(token, start, "CA", "Servers")
	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		log.Fatalf("Failed to load CA key pair: %v", err)
	}

	err = os.WriteFile(certFile, crtBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write CA certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write CA key: %v", err)
	}

	return certFile, keyFile, keyPair
}

func loadStateAgentCA(config config.Config, token []byte, start time.Time) (string, string, tls.Certificate) {
	certFile := filepath.Join(config.Dir, "agent-ca.crt")
	keyFile := filepath.Join(config.Dir, "agent-ca.key")

	crtBytes, keyBytes := createCA(token, start, "Agent-CA", "Agents")
	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		log.Fatalf("Failed to load CA key pair: %v", err)
	}

	err = os.WriteFile(certFile, crtBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write agent CA certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write agent CA key: %v", err)
	}

	return certFile, keyFile, keyPair
}

func loadStateNodeCrt(config config.Config, CA tls.Certificate) (string, string, tls.Certificate) {
	certFile := filepath.Join(config.Dir, "node.crt")
	keyFile := filepath.Join(config.Dir, "node.key")

	crtBytes, keyBytes := createNodeCrt(config, CA)

	keyPair, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		log.Fatalf("Failed to load CA key pair: %v", err)
	}

	err = os.WriteFile(certFile, crtBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write Node certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		log.Fatalf("Error: Failed to write Node key: %v", err)
	}

	return certFile, keyFile, keyPair
}

func LoadState(config config.Config) State {
	err := os.Mkdir(config.Dir, 0700)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Failed to create state dir: %v", err)
	}

	token, start := loadStateToken(config)
	caCrtFile, _, CA := loadStateCA(config, token, start)
	agentCaCrtFile, _, AgentCA := loadStateAgentCA(config, token, start)
	serverCrtFile, serverKeyFile, serverKeyPair := loadStateNodeCrt(config, CA)

	return State{
		Token:   token,
		EtcdDir: filepath.Join(config.Dir, "etcd"),

		CA:             CA,
		CACrtFile:      caCrtFile,
		AgentCA:        AgentCA,
		AgentCACrtFile: agentCaCrtFile,

		ServerKeyPair: serverKeyPair,
		ServerCrtFile: serverCrtFile,
		ServerKeyFile: serverKeyFile,
	}
}
