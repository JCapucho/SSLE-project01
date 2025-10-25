package config

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"slices"

	"github.com/caarlos0/env/v11"

	"ssle/schemas"
)

type Config struct {
	Name string `env:"NAME,required"`
	Dir  string `env:"DIR" envDefault:"state"`

	InitialToken string   `env:"TOKEN"`
	JoinUrl      *url.URL `env:"JOIN_URL"`

	PeerListenAddr        netip.Addr       `env:"PEER_LISTEN_ADDR" envDefault:"0.0.0.0"`
	PeerAdvertiseHostname schemas.Hostname `env:"PEER_ADVERTISE_HOSTNAME"`
	EtcdListenPort        uint16           `env:"ETCD_LISTEN_PORT" envDefault:"2380"`
	PeerAPIListenPort     uint16           `env:"PEER_API_LISTEN_PORT"`

	RegistryListenAddr        netip.Addr       `env:"REGISTRY_LISTEN_ADDR" envDefault:"0.0.0.0"`
	RegistryAdvertiseHostname schemas.Hostname `env:"REGISTRY_ADVERTISE_HOSTNAME"`
	RegistryAPIListenPort     uint16           `env:"REGISTRY_API_LISTEN_PORT"`
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

func (config *Config) EtcdAdvertiseHost() string {
	return config.PeerAdvertiseHostname.HostWithPort(config.EtcdListenPort)
}

func (config *Config) RegistryAPIListenHost() string {
	return schemas.HostnameFromAddr(config.RegistryListenAddr).HostWithPort(config.RegistryAPIListenPort)
}

func (config *Config) RegistryAPIAdvertiseHost() string {
	return config.RegistryAdvertiseHostname.HostWithPort(config.RegistryAPIListenPort)
}

func (config *Config) EtcdAdvertiseURLs() []url.URL {
	advertiseUrl, err := url.Parse(fmt.Sprintf("https://%v", config.EtcdAdvertiseHost()))
	if err != nil {
		log.Fatalf("Invalid peer advertise URL: %v", err)
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

	if config.JoinUrl != nil && config.InitialToken == "" {
		log.Fatal("Token must be set when joining an existing cluster")
	}

	if !config.PeerAdvertiseHostname.IsValid() {
		host := ""
		if config.JoinUrl != nil {
			host = config.JoinUrl.Hostname()
		}

		config.PeerAdvertiseHostname = schemas.HostnameFromAddr(findBestAddr(host))
		log.Printf("Peer advertise host: %v", config.PeerAdvertiseHostname)
	}

	if !config.RegistryAdvertiseHostname.IsValid() {
		config.RegistryAdvertiseHostname = config.PeerAdvertiseHostname
	}

	if config.PeerAPIListenPort == 0 {
		config.PeerAPIListenPort = config.EtcdListenPort + 1
	}

	if config.RegistryAPIListenPort == 0 {
		config.RegistryAPIListenPort = config.PeerAPIListenPort + 1
	}

	return config
}
