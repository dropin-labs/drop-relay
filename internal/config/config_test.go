package config

import (
	"os"
	"testing"
)

func TestConfig_Defaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Failed to load default config: %v", err)
	}

	if cfg.Domain != "example.com" {
		t.Errorf("Expected domain example.com, got %s", cfg.Domain)
	}
	if cfg.PortHTTP != 8080 {
		t.Errorf("Expected port 8080, got %d", cfg.PortHTTP)
	}
	if cfg.BufferType != "memory" {
		t.Errorf("Expected buffer type memory, got %s", cfg.BufferType)
	}
	if cfg.SMTPPass == "" {
		t.Errorf("Expected generated SMTPPass, got empty")
	}
}

func TestConfig_EnvOverrides(t *testing.T) {
	os.Setenv("RELAY_DOMAIN", "acme.org")
	os.Setenv("RELAY_PORT_HTTP", "9090")
	os.Setenv("BUFFER_TYPE", "redis")
	defer func() {
		os.Unsetenv("RELAY_DOMAIN")
		os.Unsetenv("RELAY_PORT_HTTP")
		os.Unsetenv("BUFFER_TYPE")
	}()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Domain != "acme.org" {
		t.Errorf("Expected domain acme.org, got %s", cfg.Domain)
	}
	if cfg.Hostname != "mail.acme.org" {
		t.Errorf("Expected hostname mail.acme.org, got %s", cfg.Hostname)
	}
	if cfg.PortHTTP != 9090 {
		t.Errorf("Expected port 9090, got %d", cfg.PortHTTP)
	}
	if cfg.BufferType != "redis" {
		t.Errorf("Expected buffer type redis, got %s", cfg.BufferType)
	}
}
