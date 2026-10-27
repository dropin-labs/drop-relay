package dispatcher

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime/multipart"
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
		fromHeader = fmt.Sprintf("\"%s\" <%s>", strings.ReplaceAll(msg.FromName, "\"", ""), msg.From)
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
	headers["Subject"] = msg.Subject
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

		// Text Part
		textHeader := make(textproto.MIMEHeader)
		textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
		textHeader.Set("Content-Transfer-Encoding", "8bit")
		partText, err := mpWriter.CreatePart(textHeader)
		if err != nil {
			return nil, err
		}
		partText.Write([]byte(msg.Text))

		// HTML Part
		htmlHeader := make(textproto.MIMEHeader)
		htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
		htmlHeader.Set("Content-Transfer-Encoding", "8bit")
		partHTML, err := mpWriter.CreatePart(htmlHeader)
		if err != nil {
			return nil, err
		}
		partHTML.Write([]byte(msg.HTML))

		if err := mpWriter.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Single-part HTML
	if msg.HTML != "" {
		headers["Content-Type"] = "text/html; charset=UTF-8"
		headers["Content-Transfer-Encoding"] = "8bit"
		for k, v := range headers {
			buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
		buf.WriteString("\r\n")
		buf.WriteString(msg.HTML)
		return buf.Bytes(), nil
	}

	// Single-part Plain Text
	headers["Content-Type"] = "text/plain; charset=UTF-8"
	headers["Content-Transfer-Encoding"] = "8bit"
	for k, v := range headers {
		buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	buf.WriteString("\r\n")
	buf.WriteString(msg.Text)
	return buf.Bytes(), nil
}
