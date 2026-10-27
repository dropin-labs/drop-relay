package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateDKIMKeyPair generates a 2048-bit RSA keypair, saves the private key to path,
// and returns the Base64 public key along with formatted DNS TXT record.
func GenerateDKIMKeyPair(privateKeyPath, selector, domain string) (pubB64 string, dnsRecord string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate RSA key: %w", err)
	}

	privASN1 := x509.MarshalPKCS1PrivateKey(key)
	privBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privASN1,
	}

	if privateKeyPath != "" {
		if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0750); err != nil {
			return "", "", fmt.Errorf("failed to create directory for dkim key: %w", err)
		}
		f, err := os.OpenFile(privateKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0400)
		if err != nil {
			return "", "", fmt.Errorf("failed to write private key: %w", err)
		}
		defer f.Close()

		if err := pem.Encode(f, privBlock); err != nil {
			return "", "", fmt.Errorf("failed to pem-encode private key: %w", err)
		}
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	pubB64 = base64.StdEncoding.EncodeToString(pubASN1)
	dnsRecord = fmt.Sprintf("v=DKIM1; k=rsa; p=%s", pubB64)
	return pubB64, dnsRecord, nil
}

// FormatChunkedTXT formats a DNS TXT string into RFC 4408 compliant 255-character chunked segments.
func FormatChunkedTXT(record string) string {
	const chunkSize = 250
	if len(record) <= chunkSize {
		return fmt.Sprintf("\"%s\"", record)
	}

	var chunks []string
	for len(record) > 0 {
		end := chunkSize
		if len(record) < end {
			end = len(record)
		}
		chunks = append(chunks, fmt.Sprintf("\"%s\"", record[:end]))
		record = record[end:]
	}
	return strings.Join(chunks, " ")
}
