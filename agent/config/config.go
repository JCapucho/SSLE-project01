package config

import (
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Token     string `env:"TOKEN"`
	TokenFile string `env:"TOKEN_FILE"`

	JoinUrl *url.URL `env:"JOIN_URL,required"`
	CAFile  string   `env:"CA_FILE,required"`

	DNSBindAddr string `env:"DNS_BIND_ADDR" envDefault:"127.0.0.143"`
	DNSUpstream string `env:"DNS_UPSTREAM" envDefault:"127.0.0.53:53"`
}

func LoadConfig() Config {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "AGENT_",
	})
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	if config.TokenFile != "" {
		token, err := os.ReadFile(config.TokenFile)
		if err != nil {
			log.Fatalf("Error loading token file: %v", err)
		}
		config.Token = strings.TrimSpace(string(token))
	}

	if config.Token == "" {
		log.Fatal("Token or token file must be set")
	}

	return config
}
