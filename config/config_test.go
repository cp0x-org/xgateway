package config

import "testing"

func TestNewProxyConfigUsesEnv(t *testing.T) {
	t.Setenv("CONFIG_FILE", "proxy.yaml")

	cfg, err := NewGatewayConfig()
	if err != nil {
		t.Fatalf("NewProxyConfig() error = %v", err)
	}

	if cfg.ConfigPath != "proxy.yaml" {
		t.Fatalf("ConfigPath = %q, want %q", cfg.ConfigPath, "proxy.yaml")
	}
}

func TestNewProxyConfigUsesDefault(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")

	cfg, err := NewGatewayConfig()
	if err != nil {
		t.Fatalf("NewProxyConfig() error = %v", err)
	}

	if cfg.ConfigPath != "config.yaml" {
		t.Fatalf("ConfigPath = %q, want %q", cfg.ConfigPath, "config.yaml")
	}
}
