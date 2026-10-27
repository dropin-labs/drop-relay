package crypto

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateDKIMKeyPair(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-dkim-*.key")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	pubB64, dnsRecord, err := GenerateDKIMKeyPair(tmpFile.Name(), "relay", "example.com")
	if err != nil {
		t.Fatalf("GenerateDKIMKeyPair failed: %v", err)
	}

	if pubB64 == "" {
		t.Errorf("Expected non-empty public key")
	}

	if !strings.HasPrefix(dnsRecord, "v=DKIM1; k=rsa; p=") {
		t.Errorf("Unexpected DNS TXT record format: %s", dnsRecord)
	}

	chunked := FormatChunkedTXT(dnsRecord)
	if !strings.Contains(chunked, "\" \"") {
		t.Errorf("Expected chunked TXT to contain split segments, got: %s", chunked)
	}

	keyData, err := os.ReadFile(tmpFile.Name())
	if err != nil || !strings.Contains(string(keyData), "-----BEGIN RSA PRIVATE KEY-----") {
		t.Errorf("Private key file not written properly: %v", err)
	}
}
