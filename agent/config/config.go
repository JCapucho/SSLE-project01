package config

import (
	"log"
	"net/url"
	"os"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Name    string   `env:"NAME"`
	Token   string   `env:"TOKEN,required"`
	JoinUrl *url.URL `env:"JOIN_URL,required"`
	CAFile  string   `env:"CA_FILE,required"`
}

func LoadConfig() Config {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "AGENT_",
	})
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	if config.Name == "" {
		config.Name, err = os.Hostname()
		if err != nil {
			log.Fatalf("No name provided and couldn't retrieve hostname: %v", err)
		}
	}

	return config
}
