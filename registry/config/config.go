package config

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/caarlos0/env/v11"

	"ssle/registry/schemas"
)

type Config struct {
	Name string `env:"NAME,required"`
	Dir  string `env:"DIR" envDefault:"state"`

	InitialToken     string `env:"TOKEN"`
	InitialTokenFile string `env:"TOKEN_FILE"`
	JoinUrl          string `env:"JOIN_URL"`

	PeerListenAddr        netip.Addr       `env:"PEER_LISTEN_ADDR" envDefault:"0.0.0.0"`
	PeerAdvertiseHostname schemas.Hostname `env:"PEER_ADVERTISE_HOSTNAME"`
	EtcdListenPort        uint16           `env:"ETCD_LISTEN_PORT" envDefault:"2380"`
	EtcdClientListenPort  uint16           `env:"ETCD_CLIENT_LISTEN_PORT"`
	PeerAPIListenPort     uint16           `env:"PEER_API_LISTEN_PORT"`

	AgentAPIListenAddr        netip.Addr       `env:"AGENT_LISTEN_ADDR" envDefault:"0.0.0.0"`
	AgentAPIAdvertiseHostname schemas.Hostname `env:"AGENT_ADVERTISE_HOSTNAME"`
	AgentAPIListenPort        uint16           `env:"AGENT_API_LISTEN_PORT"`
}

func (config *Config) PeerAPIListenHost() string {
	return schemas.HostnameFromAddr(config.PeerListenAddr).HostWithPort(config.PeerAPIListenPort)
}

func (config *Config) PeerAPIAdvertiseHost() string {
	return config.PeerAdvertiseHostname.HostWithPort(config.PeerAPIListenPort)
}

func (config *Config) EtcdListenHost() string {
	return schemas.HostnameFromAddr(config.PeerListenAddr).HostWithPort(config.EtcdListenPort)
}

func (config *Config) EtcdClientListenHost() string {
	return schemas.HostnameFromAddr(config.PeerListenAddr).HostWithPort(config.EtcdClientListenPort)
}

func (config *Config) EtcdAdvertiseHost() string {
	return config.PeerAdvertiseHostname.HostWithPort(config.EtcdListenPort)
}

func (config *Config) EtcdClientAdvertiseHost() string {
	return config.PeerAdvertiseHostname.HostWithPort(config.EtcdClientListenPort)
}

func (config *Config) AgentAPIListenHost() string {
	return schemas.HostnameFromAddr(config.AgentAPIListenAddr).HostWithPort(config.AgentAPIListenPort)
}

func (config *Config) AgentAPIAdvertiseHost() string {
	return config.AgentAPIAdvertiseHostname.HostWithPort(config.AgentAPIListenPort)
}

func (config *Config) EtcdAdvertiseURLs() []url.URL {
	advertiseUrl, err := url.Parse(fmt.Sprintf("https://%v", config.EtcdAdvertiseHost()))
	if err != nil {
		log.Fatalf("Invalid peer advertise URL: %v", err)
	}
	return []url.URL{*advertiseUrl}
}

func (config *Config) EtcdClientAdvertiseURLs() []url.URL {
	advertiseUrl, err := url.Parse(fmt.Sprintf("https://%v", config.EtcdClientAdvertiseHost()))
	if err != nil {
		log.Fatalf("Invalid peer client advertise URL: %v", err)
	}
	return []url.URL{*advertiseUrl}
}

func (config *Config) EtcdListenURLs() []url.URL {
	listenUrl, err := url.Parse(fmt.Sprintf("https://%v", config.EtcdListenHost()))
	if err != nil {
		log.Fatalf("Invalid peer listen URL: %v", err)
	}
	return []url.URL{*listenUrl}
}

func (config *Config) EtcdClientListenURLs() []url.URL {
	listenUrl, err := url.Parse(fmt.Sprintf("https://%v", config.EtcdClientListenHost()))
	if err != nil {
		log.Fatalf("Invalid peer client listen URL: %v", err)
	}
	return []url.URL{*listenUrl}
}

func IsIPv4(ip net.IP) bool {
	return ip.To4() != nil
}

func ipToNetIPAddr(ip net.IP) netip.Addr {
	if IsIPv4(ip) {
		ip2, _ := netip.AddrFromSlice(ip[12:])
		return ip2
	} else {
		ip2, _ := netip.AddrFromSlice(ip)
		return ip2
	}
}

func findBestAddr(host string) netip.Addr {
	var err error

	joinAddrs := []net.IP{}
	ipv4Unsupported := host != ""
	ipv6Unsupported := host != ""

	if host != "" {
		joinAddrs, err = net.LookupIP(host)
		if err != nil {
			log.Fatalf("Failed to resolve join url: %v", err)
		}

		for _, addr := range joinAddrs {
			if IsIPv4(addr) {
				ipv4Unsupported = false
			} else {
				ipv6Unsupported = false
			}
		}
	}

	var bestIP netip.Addr
	addrs, _ := net.InterfaceAddrs()

	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			// If this interface is in the same subnet select it
			if slices.ContainsFunc(joinAddrs, v.Contains) {
				return ipToNetIPAddr(v.IP)
			}

			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		// Skip link local addresses
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}

		isIPv4 := IsIPv4(ip)

		// Select only ip families compatible with the host
		if isIPv4 && ipv4Unsupported {
			continue
		}

		if !isIPv4 && ipv6Unsupported {
			continue
		}

		// Prefer IPv4 addresses
		if bestIP.Is6() && isIPv4 || !bestIP.IsValid() {
			bestIP = ipToNetIPAddr(ip)
		}
	}

	return bestIP
}

func LoadConfig() Config {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "REGISTRY_",
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf(schemas.Hostname{}): func(v string) (any, error) {
				return schemas.ParseHostname(v)
			},
		},
	})
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	if config.InitialTokenFile != "" {
		token, err := os.ReadFile(config.InitialTokenFile)
		if err != nil {
			log.Fatalf("Error loading token file: %v", err)
		}
		config.InitialToken = strings.TrimSpace(string(token))
	}

	if config.JoinUrl != "" && config.InitialToken == "" {
		log.Fatal("Token or token file must be set when joining an existing cluster")
	}

	if !config.PeerAdvertiseHostname.IsValid() {
		host := ""
		if config.JoinUrl != "" {
			host, _, err = net.SplitHostPort(config.JoinUrl)
			if err != nil {
				log.Fatalf("Invalid join url: %v", err)
			}
		}

		config.PeerAdvertiseHostname = schemas.HostnameFromAddr(findBestAddr(host))
		log.Printf("Etcd advertise addr: %v", config.EtcdAdvertiseHost())
	}

	if !config.AgentAPIAdvertiseHostname.IsValid() {
		config.AgentAPIAdvertiseHostname = config.PeerAdvertiseHostname
	}

	if config.EtcdClientListenPort == 0 {
		config.EtcdClientListenPort = config.EtcdListenPort + 1
		log.Printf("Etcd client advertise addr: %v", config.EtcdClientAdvertiseHost())
	}

	if config.PeerAPIListenPort == 0 {
		config.PeerAPIListenPort = config.EtcdClientListenPort + 1
		log.Printf("Peer api advertise host: %v", config.PeerAPIAdvertiseHost())
	}

	if config.AgentAPIListenPort == 0 {
		config.AgentAPIListenPort = config.PeerAPIListenPort + 1
	}

	return config
}
