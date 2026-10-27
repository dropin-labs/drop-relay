package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config encapsulates all runtime configuration for drop-relay.
type Config struct {
	// Identity & Network
	Domain   string `yaml:"domain"`
	Hostname string `yaml:"hostname"`
	PortHTTP int    `yaml:"port_http"`
	APIKey   string `yaml:"api_key"`

	// Submission Authentication (Port 587)
	SMTPUser string `yaml:"smtp_user"`
	SMTPPass string `yaml:"smtp_pass"`

	// Submission Target for local relay dispatcher
	SubmissionHost string `yaml:"submission_host"`

	// Cryptography (DKIM)
	DKIMSelector string `yaml:"dkim_selector"`
	DKIMKeyPath  string `yaml:"dkim_key_path"`

	// Inbound Webhook (Port 25)
	InboundWebhookURL    string `yaml:"inbound_webhook_url"`
	RelayIntegritySecret string `yaml:"integrity_secret"`

	// Optional Upstream Smart-Host Relay
	UpstreamSmartHost string `yaml:"upstream_smart_host"`
	UpstreamAuthUser  string `yaml:"upstream_auth_user"`
	UpstreamAuthPass  string `yaml:"upstream_auth_pass"`

	// TLS Certificates
	TLSCertPath string `yaml:"tls_cert_path"`
	TLSKeyPath  string `yaml:"tls_key_path"`

	// Message Policy & Buffer
	MaxMessageSize int64  `yaml:"max_message_size"`
	BufferType     string `yaml:"buffer_type"` // "memory" or "redis"
	BufferCapacity int    `yaml:"buffer_capacity"`

	// Redis Config (if BufferType == "redis")
	RedisURL    string `yaml:"redis_url"`
	RedisStream string `yaml:"redis_stream"`
	RedisGroup  string `yaml:"redis_group"`

	// Logging & Metrics
	LogLevel string `yaml:"log_level"`
}

// Load loads the configuration from environment variables and an optional YAML config file path.
// Precedence: Defaults -> YAML file -> Environment variables.
func Load(configPath string) (*Config, error) {
	cfg := &Config{
		Domain:               "example.com",
		Hostname:             "mail.example.com",
		PortHTTP:             8080,
		APIKey:               "",
		SMTPUser:             "relay_user",
		SMTPPass:             "",
		SubmissionHost:       "127.0.0.1:587",
		DKIMSelector:         "relay",
		DKIMKeyPath:          "/etc/opendkim/keys/relay.private",
		InboundWebhookURL:    "",
		RelayIntegritySecret: "",
		UpstreamSmartHost:    "",
		UpstreamAuthUser:     "",
		UpstreamAuthPass:     "",
		TLSCertPath:          "",
		TLSKeyPath:           "",
		MaxMessageSize:       26214400, // 25 MB
		BufferType:           "memory",
		BufferCapacity:       10000,
		RedisURL:             "redis://127.0.0.1:6379",
		RedisStream:          "drop_relay_spool",
		RedisGroup:           "relay_workers",
		LogLevel:             "info",
	}

	// 1. Load YAML file if exists
	if configPath != "" {
		if err := loadYAML(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	} else if _, err := os.Stat("config/config.yaml"); err == nil {
		if err := loadYAML("config/config.yaml", cfg); err != nil {
			return nil, fmt.Errorf("failed to parse default config.yaml: %w", err)
		}
	}

	// 2. Override from Environment Variables
	if v := os.Getenv("RELAY_DOMAIN"); v != "" {
		cfg.Domain = strings.TrimSpace(v)
	}
	if v := os.Getenv("RELAY_HOSTNAME"); v != "" {
		cfg.Hostname = strings.TrimSpace(v)
	} else if cfg.Hostname == "mail.example.com" && cfg.Domain != "example.com" {
		cfg.Hostname = "mail." + cfg.Domain
	}

	if v := os.Getenv("RELAY_PORT_HTTP"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.PortHTTP = p
		}
	}
	if v := os.Getenv("RELAY_API_KEY"); v != "" {
		cfg.APIKey = strings.TrimSpace(v)
	}

	if v := os.Getenv("SMTP_USER"); v != "" {
		cfg.SMTPUser = strings.TrimSpace(v)
	}
	if v := os.Getenv("SMTP_PASS"); v != "" {
		cfg.SMTPPass = strings.TrimSpace(v)
	}
	if v := os.Getenv("SUBMISSION_HOST"); v != "" {
		cfg.SubmissionHost = strings.TrimSpace(v)
	}

	if v := os.Getenv("DKIM_SELECTOR"); v != "" {
		cfg.DKIMSelector = strings.TrimSpace(v)
	}
	if v := os.Getenv("DKIM_KEY_PATH"); v != "" {
		cfg.DKIMKeyPath = strings.TrimSpace(v)
	}

	if v := os.Getenv("INBOUND_WEBHOOK_URL"); v != "" {
		cfg.InboundWebhookURL = strings.TrimSpace(v)
	}
	if v := os.Getenv("RELAY_INTEGRITY_SECRET"); v != "" {
		cfg.RelayIntegritySecret = strings.TrimSpace(v)
	}

	if v := os.Getenv("UPSTREAM_SMART_HOST"); v != "" {
		cfg.UpstreamSmartHost = strings.TrimSpace(v)
	}
	if v := os.Getenv("UPSTREAM_AUTH_USER"); v != "" {
		cfg.UpstreamAuthUser = strings.TrimSpace(v)
	}
	if v := os.Getenv("UPSTREAM_AUTH_PASS"); v != "" {
		cfg.UpstreamAuthPass = strings.TrimSpace(v)
	}

	if v := os.Getenv("TLS_CERT_PATH"); v != "" {
		cfg.TLSCertPath = strings.TrimSpace(v)
	}
	if v := os.Getenv("TLS_KEY_PATH"); v != "" {
		cfg.TLSKeyPath = strings.TrimSpace(v)
	}

	if v := os.Getenv("MAX_MESSAGE_SIZE"); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil && s > 0 {
			cfg.MaxMessageSize = s
		}
	}

	if v := os.Getenv("BUFFER_TYPE"); v != "" {
		cfg.BufferType = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("BUFFER_CAPACITY"); v != "" {
		if c, err := strconv.Atoi(v); err == nil && c > 0 {
			cfg.BufferCapacity = c
		}
	}

	if v := os.Getenv("REDIS_URL"); v != "" {
		cfg.RedisURL = strings.TrimSpace(v)
	}
	if v := os.Getenv("REDIS_STREAM"); v != "" {
		cfg.RedisStream = strings.TrimSpace(v)
	}
	if v := os.Getenv("REDIS_GROUP"); v != "" {
		cfg.RedisGroup = strings.TrimSpace(v)
	}

	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToLower(strings.TrimSpace(v))
	}

	// 3. Fallbacks and defaults generation
	if cfg.SMTPPass == "" {
		cfg.SMTPPass = generateRandomHex(16)
	}
	if cfg.RelayIntegritySecret == "" {
		cfg.RelayIntegritySecret = generateRandomHex(32)
	}

	return cfg, nil
}

func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

func generateRandomHex(bytesCount int) string {
	b := make([]byte, bytesCount)
	if _, err := rand.Read(b); err != nil {
		return "fallback_insecure_random_key"
	}
	return hex.EncodeToString(b)
}
