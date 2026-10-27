package dispatcher

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/dropin-labs/drop-relay/internal/buffer"
	"github.com/dropin-labs/drop-relay/internal/config"
	"github.com/dropin-labs/drop-relay/internal/metrics"
)

// Dispatcher processes spooled messages from the buffer and delivers them to the submission port.
type Dispatcher struct {
	cfg         *config.Config
	buffer      buffer.Buffer
	concurrency int
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewDispatcher initializes the background delivery worker pool.
func NewDispatcher(cfg *config.Config, buf buffer.Buffer, concurrency int) *Dispatcher {
	if concurrency <= 0 {
		concurrency = 5
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		cfg:         cfg,
		buffer:      buf,
		concurrency: concurrency,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins processing messages concurrently in background goroutines.
func (d *Dispatcher) Start() {
	for i := 0; i < d.concurrency; i++ {
		d.wg.Add(1)
		go d.worker(i)
	}
}

// Stop signals workers to finish processing and waits for current in-flight dispatches to complete.
func (d *Dispatcher) Stop(timeout time.Duration) {
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (d *Dispatcher) worker(_ int) {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		msg, err := d.buffer.Dequeue(d.ctx)
		if err != nil {
			if d.ctx.Err() != nil || err == buffer.ErrBufferClosed {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if msg == nil {
			continue
		}

		// Safeguard against prematurely dequeued scheduled messages
		if !msg.SendAt.IsZero() && time.Now().UTC().Before(msg.SendAt) {
			delay := time.Until(msg.SendAt)
			if delay > 0 && delay <= 200*time.Millisecond {
				select {
				case <-d.ctx.Done():
					return
				case <-time.After(delay):
				}
			} else {
				// Re-enqueue after remaining duration so worker is not blocked
				go func(m *buffer.Message, waitDur time.Duration) {
					time.Sleep(waitDur)
					_ = d.buffer.Enqueue(context.Background(), m)
				}(msg, delay)
				_ = d.buffer.Ack(context.Background(), msg.ID)
				continue
			}
		}

		start := time.Now()
		err = d.deliverMessage(msg)
		duration := time.Since(start).Seconds()
		metrics.DeliveryDuration.Observe(duration)

		if err != nil {
			metrics.DeliveryFailuresTotal.WithLabelValues("smtp_error").Inc()
			_ = d.buffer.Nack(context.Background(), msg.ID, err)
		} else {
			metrics.DeliveredTotal.WithLabelValues("sent").Inc()
			_ = d.buffer.Ack(context.Background(), msg.ID)
		}
	}
}

func (d *Dispatcher) deliverMessage(msg *buffer.Message) error {
	rawBytes := msg.Raw
	if len(rawBytes) == 0 {
		var err error
		rawBytes, err = d.buildMIMEMessage(msg)
		if err != nil {
			return fmt.Errorf("failed to build MIME payload: %w", err)
		}
	}

	host, _, err := net.SplitHostPort(d.cfg.SubmissionHost)
	if err != nil {
		host = d.cfg.SubmissionHost
	}

	// Connect to local Postfix Submission Port (:587)
	conn, err := net.DialTimeout("tcp", d.cfg.SubmissionHost, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to submission host %s: %w", d.cfg.SubmissionHost, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Quit()

	// Opportunistic or Enforced STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true, // Internal container hop
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("failed STARTTLS negotiation: %w", err)
		}
	}

	// Cyrus SASL Authentication if enabled
	if d.cfg.SMTPUser != "" && d.cfg.SMTPPass != "" {
		auth := smtp.PlainAuth("", d.cfg.SMTPUser, d.cfg.SMTPPass, host)
		if ok, _ := client.Extension("AUTH"); ok {
			if err = client.Auth(auth); err != nil {
				return fmt.Errorf("SASL authentication rejected for %s: %w", d.cfg.SMTPUser, err)
			}
		}
	}

	if err = client.Mail(msg.From); err != nil {
		return fmt.Errorf("MAIL FROM <%s> rejected: %w", msg.From, err)
	}

	allRecipients := append(append([]string{}, msg.To...), msg.Cc...)
	allRecipients = append(allRecipients, msg.Bcc...)

	for _, rcpt := range allRecipients {
		if strings.TrimSpace(rcpt) != "" {
			if err = client.Rcpt(strings.TrimSpace(rcpt)); err != nil {
				return fmt.Errorf("RCPT TO <%s> rejected: %w", rcpt, err)
			}
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command rejected: %w", err)
	}

	if _, err = w.Write(rawBytes); err != nil {
		_ = w.Close()
		return fmt.Errorf("failed writing message payload: %w", err)
	}

	return w.Close()
}

func (d *Dispatcher) buildMIMEMessage(msg *buffer.Message) ([]byte, error) {
	var buf bytes.Buffer

	// Headers
	fromHeader := msg.From
	if msg.FromName != "" {
		cleanName := strings.ReplaceAll(msg.FromName, "\"", "")
		if !isASCII(cleanName) {
			fromHeader = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", cleanName), msg.From)
		} else {
			fromHeader = fmt.Sprintf("\"%s\" <%s>", cleanName, msg.From)
		}
	}

	headers := make(map[string]string)
	for k, v := range msg.Headers {
		headers[k] = v
	}

	headers["From"] = fromHeader
	headers["To"] = strings.Join(msg.To, ", ")
	if len(msg.Cc) > 0 {
		headers["Cc"] = strings.Join(msg.Cc, ", ")
	}
	headers["Subject"] = encodeHeaderWord(msg.Subject)
	headers["Date"] = time.Now().UTC().Format(time.RFC1123Z)
	if _, ok := headers["Message-ID"]; !ok {
		headers["Message-ID"] = fmt.Sprintf("<%s@%s>", msg.ID, d.cfg.Domain)
	}
	headers["MIME-Version"] = "1.0"

	// Multipart Alternative if both HTML and Text exist
	if msg.HTML != "" && msg.Text != "" {
		mpWriter := multipart.NewWriter(&buf)
		headers["Content-Type"] = fmt.Sprintf("multipart/alternative; boundary=\"%s\"", mpWriter.Boundary())

		for k, v := range headers {
			buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
		buf.WriteString("\r\n")

		// Text Part (Quoted-Printable for robust non-ASCII & line wrapping)
		textHeader := make(textproto.MIMEHeader)
		textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
		textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		partText, err := mpWriter.CreatePart(textHeader)
		if err != nil {
			return nil, err
		}
		partText.Write(encodeQuotedPrintable(msg.Text))

		// HTML Part (Quoted-Printable for robust non-ASCII & line wrapping)
		htmlHeader := make(textproto.MIMEHeader)
		htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
		htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		partHTML, err := mpWriter.CreatePart(htmlHeader)
		if err != nil {
			return nil, err
		}
		partHTML.Write(encodeQuotedPrintable(msg.HTML))

		if err := mpWriter.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Single-part HTML
	if msg.HTML != "" {
		headers["Content-Type"] = "text/html; charset=UTF-8"
		headers["Content-Transfer-Encoding"] = "quoted-printable"
		for k, v := range headers {
			buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
		buf.WriteString("\r\n")
		buf.Write(encodeQuotedPrintable(msg.HTML))
		return buf.Bytes(), nil
	}

	// Single-part Plain Text
	headers["Content-Type"] = "text/plain; charset=UTF-8"
	headers["Content-Transfer-Encoding"] = "quoted-printable"
	for k, v := range headers {
		buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	buf.WriteString("\r\n")
	buf.Write(encodeQuotedPrintable(msg.Text))
	return buf.Bytes(), nil
}

// encodeHeaderWord encodes a header string using RFC 2047 Q-Encoding if it contains non-ASCII runes.
func encodeHeaderWord(s string) string {
	if isASCII(s) {
		return s
	}
	return mime.QEncoding.Encode("utf-8", s)
}

// isASCII returns true if all bytes in s are within standard 7-bit US-ASCII range.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// encodeQuotedPrintable encodes string payload into quoted-printable bytes conforming to RFC 2045/5322.
func encodeQuotedPrintable(s string) []byte {
	var buf bytes.Buffer
	qp := quotedprintable.NewWriter(&buf)
	_, _ = qp.Write([]byte(s))
	_ = qp.Close()
	return buf.Bytes()
}
