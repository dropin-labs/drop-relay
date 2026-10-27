package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dropin-labs/drop-relay/internal/buffer"
	"github.com/dropin-labs/drop-relay/internal/config"
	"github.com/dropin-labs/drop-relay/internal/crypto"
	"github.com/dropin-labs/drop-relay/internal/dispatcher"
	relayHTTP "github.com/dropin-labs/drop-relay/internal/http"
)

var Version = "0.1.2"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "generate-dkim":
			runGenerateDKIM(os.Args[2:])
			return
		case "version", "--version", "-v":
			fmt.Printf("drop-relay v%s\n", Version)
			return
		}
	}

	runServer()
}

func runServer() {
	configPath := flag.String("config", "", "Path to YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[FATAL] Configuration load failed: %v", err)
	}

	log.Printf("[INFO] Initializing drop-relay v%s (domain=%s, hostname=%s, buffer=%s)",
		Version, cfg.Domain, cfg.Hostname, cfg.BufferType)

	// Initialize Buffer Engine
	buf, err := buffer.NewBuffer(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize buffer engine: %v", err)
	}
	defer buf.Close()

	// Initialize and Start Dispatcher Worker Pool
	disp := dispatcher.NewDispatcher(cfg, buf, 5)
	disp.Start()
	log.Printf("[INFO] Delivery dispatcher pool started (target=%s)", cfg.SubmissionHost)

	// Initialize HTTP Server
	srv := relayHTTP.NewServer(cfg, buf)

	// Graceful shutdown handling
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[INFO] Ingestion API listening on :%d", cfg.PortHTTP)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] HTTP server error: %v", err)
		}
	}()

	sig := <-stopCh
	log.Printf("[INFO] Received signal %v. Initiating graceful shutdown...", sig)

	// 1. Terminate HTTP ingress
	ctxShut, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShut()
	_ = srv.Close()
	_ = ctxShut

	// 2. Flush in-flight spool messages
	log.Printf("[INFO] Draining active spool buffer (grace period: 10s)...")
	disp.Stop(10 * time.Second)

	// 3. Close buffer
	_ = buf.Close()

	log.Printf("[INFO] drop-relay shutdown complete.")
}

func runGenerateDKIM(args []string) {
	fs := flag.NewFlagSet("generate-dkim", flag.ExitOnError)
	selector := fs.String("selector", "relay", "DKIM DNS selector")
	domain := fs.String("domain", "example.com", "Signing domain")
	keyPath := fs.String("out", "/etc/opendkim/keys/relay.private", "Path to save private key")
	_ = fs.Parse(args)

	// Override from env if present
	if envDom := os.Getenv("RELAY_DOMAIN"); envDom != "" && *domain == "example.com" {
		*domain = envDom
	}
	if envSel := os.Getenv("DKIM_SELECTOR"); envSel != "" && *selector == "relay" {
		*selector = envSel
	}
	if envOut := os.Getenv("DKIM_KEY_PATH"); envOut != "" && *keyPath == "/etc/opendkim/keys/relay.private" {
		*keyPath = envOut
	}

	pubB64, dnsRecord, err := crypto.GenerateDKIMKeyPair(*keyPath, *selector, *domain)
	if err != nil {
		log.Fatalf("DKIM generation failed: %v", err)
	}

	chunkedRecord := crypto.FormatChunkedTXT(dnsRecord)

	fmt.Printf("\n=== DKIM Key Generation Successful ===\n")
	fmt.Printf("Private Key Path: %s\n", *keyPath)
	fmt.Printf("Selector:         %s\n", *selector)
	fmt.Printf("Domain:           %s\n\n", *domain)
	fmt.Printf("--- DNS TXT Record (Publish at %s._domainkey.%s) ---\n", *selector, *domain)
	fmt.Printf("Host/Name: %s._domainkey\n", *selector)
	fmt.Printf("Type:      TXT\n")
	fmt.Printf("Value (Single Line):\n  %s\n\n", dnsRecord)
	fmt.Printf("Value (RFC 4408 Split/Chunked Format for BIND/Cloudflare):\n  %s\n\n", chunkedRecord)
	fmt.Printf("Raw Base64 Public Key: %s\n\n", pubB64)
}
