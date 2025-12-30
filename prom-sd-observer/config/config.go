package config

import (
	"log"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Dir string `env:"DIR" envDefault:"observer.state"`

	CrtFile string `env:"CERTIFICATE" envDefault:"node.crt"`
	KeyFile string `env:"KEY" envDefault:"node.key"`

	JoinUrl string `env:"JOIN_URL,required"`
	CAFile  string `env:"CA_FILE" envDefault:"ca.crt"`

	TargetsFile string `env:"TARGETS_FILE" envDefault:"targets.json"`
}

func LoadConfig() Config {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "OBSERVER_",
	})
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	return config
}
