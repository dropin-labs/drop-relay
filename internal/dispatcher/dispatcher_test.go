package dispatcher

import (
	"bytes"
	"mime"
	"strings"
	"testing"

	"github.com/dropin-labs/drop-relay/internal/buffer"
	"github.com/dropin-labs/drop-relay/internal/config"
)

func TestBuildMIMEMessage_ASCII(t *testing.T) {
	d := &Dispatcher{cfg: &config.Config{Domain: "example.com"}}
	msg := &buffer.Message{
		ID:       "msg_123",
		From:     "sender@example.com",
		FromName: "Alice Sender",
		To:       []string{"recipient@example.com"},
		Subject:  "Standard ASCII Subject",
		Text:     "Hello world, plain ASCII text.",
		HTML:     "<p>Hello world, plain ASCII text.</p>",
	}

	raw, err := d.buildMIMEMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := string(raw)
	if !strings.Contains(content, "Subject: Standard ASCII Subject\r\n") {
		t.Errorf("expected clean ASCII subject, got:\n%s", content)
	}
	if !strings.Contains(content, `From: "Alice Sender" <sender@example.com>`) {
		t.Errorf("expected clean ASCII from header, got:\n%s", content)
	}
	if !strings.Contains(content, "Content-Transfer-Encoding: quoted-printable") {
		t.Errorf("expected quoted-printable transfer encoding, got:\n%s", content)
	}
}

func TestBuildMIMEMessage_UnicodeAndEmojis(t *testing.T) {
	d := &Dispatcher{cfg: &config.Config{Domain: "example.com"}}
	msg := &buffer.Message{
		ID:       "msg_unicode",
		From:     "ops@example.rw",
		FromName: "Équipe Développeur 🚀",
		To:       []string{"client@target.rw"},
		Subject:  "Muraho neza! Rendez-vous à 15h 🚀",
		Text:     "Muraho! Ibi ni ubutumwa burimo inyuguti zidasanzwe: é, è, à, î, cyangwa ubutumwa bw'ikinyarwanda.",
		HTML:     "<p>Muraho! Ibi ni ubutumwa burimo inyuguti zidasanzwe: <strong>é, è, à, î</strong> 🚀.</p>",
	}

	raw, err := d.buildMIMEMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := string(raw)

	// Verify RFC 2047 Q-Encoding in Subject
	if !strings.Contains(content, "Subject: =?utf-8?q?") && !strings.Contains(content, "Subject: =?utf-8?Q?") {
		t.Errorf("expected RFC 2047 Q-encoded subject, got:\n%s", content)
	}

	// Verify From header RFC 2047 encoding for non-ASCII display name
	if !strings.Contains(content, "=?utf-8?q?") && !strings.Contains(content, "=?utf-8?Q?") {
		t.Errorf("expected RFC 2047 encoded From header, got:\n%s", content)
	}

	// Verify Quoted-Printable encoding in body
	if !strings.Contains(content, "Content-Transfer-Encoding: quoted-printable") {
		t.Errorf("expected quoted-printable transfer encoding")
	}

	// Decode the Subject to ensure it preserves the exact original string
	var subjectLine string
	for _, line := range strings.Split(content, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subjectLine = strings.TrimPrefix(line, "Subject: ")
			break
		}
	}

	dec := new(mime.WordDecoder)
	decodedSubject, err := dec.DecodeHeader(subjectLine)
	if err != nil {
		t.Fatalf("failed decoding subject header '%s': %v", subjectLine, err)
	}

	if decodedSubject != msg.Subject {
		t.Errorf("decoded subject mismatch.\nGot:  %s\nWant: %s", decodedSubject, msg.Subject)
	}
}

func TestBuildMIMEMessage_SinglePartQuotedPrintable(t *testing.T) {
	d := &Dispatcher{cfg: &config.Config{Domain: "example.com"}}
	msg := &buffer.Message{
		ID:      "msg_single",
		From:    "sender@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "Single Part Test",
		Text:    "Ceci est un test avec des caractères accentués: café, été, naïve.",
	}

	raw, err := d.buildMIMEMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := string(raw)
	if !strings.Contains(content, "Content-Transfer-Encoding: quoted-printable") {
		t.Errorf("expected quoted-printable in single part text")
	}
	// Quoted-printable encodes é as =C3=A9
	if !bytes.Contains(raw, []byte("=C3=A9")) {
		t.Errorf("expected quoted-printable representation =C3=A9 in payload:\n%s", content)
	}
}
