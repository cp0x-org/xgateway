package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

const DefaultEnvFile = ".env"

const (
	ProviderFile = "file"
	ProviderHTTP = "http"
)

type GatewayConfig struct {
	ConfigProvider string `env:"CONFIG_PROVIDER" envDefault:"file"`
	ConfigPath     string `env:"CONFIG_FILE" envDefault:"config.yaml"`
	Port           int    `env:"PORT" envDefault:"8080"`
	Mode           string `env:"GIN_MODE" envDefault:"release"`
	Debug          bool   `env:"DEBUG" envDefault:"false"`

	// Required when CONFIG_PROVIDER=http
	ControlAPIURL  string `env:"CONTROL_API_URL"`
	InternalAPIKey string `env:"INTERNAL_API_KEY"`
}

func NewGatewayConfig() (*GatewayConfig, error) {
	cfg := &GatewayConfig{}
	if err := parse(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *GatewayConfig) Validate() error {
	provider := strings.ToLower(strings.TrimSpace(c.ConfigProvider))
	switch provider {
	case ProviderFile:
		if strings.TrimSpace(c.ConfigPath) == "" {
			return errors.New("CONFIG_FILE is required when CONFIG_PROVIDER=file")
		}
	case ProviderHTTP:
		var errs []string
		if strings.TrimSpace(c.ControlAPIURL) == "" {
			errs = append(errs, "CONTROL_API_URL")
		}
		if strings.TrimSpace(c.InternalAPIKey) == "" {
			errs = append(errs, "INTERNAL_API_KEY")
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s required when CONFIG_PROVIDER=http", strings.Join(errs, ", "))
		}
	default:
		return fmt.Errorf("CONFIG_PROVIDER must be %q or %q, got %q", ProviderFile, ProviderHTTP, c.ConfigProvider)
	}
	return nil
}

func parse(cfg any) error {
	return env.Parse(cfg)
}

func LoadEnv(envFile string) error {
	if envFile != "" {
		return godotenv.Load(envFile)
	}

	if _, err := os.Stat(DefaultEnvFile); err == nil {
		return godotenv.Load(DefaultEnvFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}
