package config

import (
	"log"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Dir string `env:"DIR" envDefault:"agent.state"`

	CrtFile string `env:"CERTIFICATE" envDefault:"agent.crt"`
	KeyFile string `env:"KEY" envDefault:"agent.key"`

	JoinUrl string `env:"JOIN_URL,required"`
	CAFile  string `env:"CA_FILE" envDefault:"ca.crt"`

	DNSBindAddr string `env:"DNS_BIND_ADDR" envDefault:"127.0.0.143"`
	DNSUpstream string `env:"DNS_UPSTREAM" envDefault:"127.0.0.53:53"`

	SigningIssuer string `env:"SIGNING_ISSUER" envDefault:"https://github.com/login/oauth"`
	SigningSAN    string `env:"SIGNING_SAN" envDefault:"^capucho@jcapucho\\.com$"`
}

func LoadConfig() Config {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "AGENT_",
	})
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	return config
}
