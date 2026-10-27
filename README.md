# drop-relay

[![CI/CD Pipeline](https://github.com/dropin-labs/drop-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/dropin-labs/drop-relay/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/dropin-labs/drop-relay?color=22c55e)](https://github.com/dropin-labs/drop-relay/releases)
[![Container Image](https://img.shields.io/badge/ghcr.io-dropin--labs%2Fdrop--relay-blue?logo=docker)](https://github.com/dropin-labs/drop-relay/pkgs/container/drop-relay)
[![Container Size](https://img.shields.io/badge/container%20size-32MB-informational)](docker/Dockerfile)
[![Trivy Security](https://img.shields.io/badge/trivy%20audit-0%20CVEs-brightgreen)](SECURITY.md)
[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

> High-throughput, self-contained mail transfer agent (MTA), automated DKIM signing engine, and bidirectional delivery proxy for modern container architectures.

`drop-relay` extracts and generalizes production email delivery into an unopinionated, zero-bloat systems utility. It provides a dual-interface architecture: a modern high-throughput HTTP ingestion API (`POST /v1/send`) alongside a standard RFC 5322/6409 SMTP submission daemon (`:587`), backed by pluggable bounded spool buffers and an inbound webhook pipe bridge (`:25`).

---

## Architecture at a Glance

```
                ┌─────────────────────────────────────────────────────────────┐
                │                         drop-relay                          │
                │                                                             │
HTTP /v1/send ─►│ [Ingest API] ──► [Spool Buffer] ──► [OpenDKIM] ──► [Postfix]├─► Remote MX / Smart-Host
SMTP Submission►│ (Port 587)          (Mem/Redis)       (RSA-2048)  │(Port 25)│
Inbound MX ────►│ [Postfix :25] ──────────────────► [HMAC Bridge] ────────────┼─► HTTP Inbound Webhook
                └─────────────────────────────────────────────────────────────┘
```

For complete technical specifications, retry backoff equations, and kernel socket topologies, read the **[Architecture Specification](ARCHITECTURE.md)**.

---

## Key Features

* **Dual Ingestion Engine**: Accept mail via JSON REST API (`POST /v1/send`) or standard RFC 6409 SMTP submission (`:587`) with Cyrus SASL authentication.
* **Bounded Spool Buffers**: Zero-dependency bounded in-memory ring buffer with backpressure (`503 Service Unavailable` on saturation) or distributed Redis Streams backend.
* **Inbound Webhook Pipe Bridge**: Listens on Port 25, captures raw `.eml` envelopes, signs payloads with HMAC-SHA256 (`X-Relay-Signature`), and dispatches to HTTP webhooks with POSIX `<sysexits.h>` exit code mapping (`0` OK, `67` Hard Bounce, `75` Deferral).
* **Automated Cryptography**: Automatic 2048-bit RSA DKIM keygen (`relay generate-dkim`), multi-tenant dynamic table hydration, and RFC 4408 chunked DNS TXT formatting.
* **Header Hygiene & Privacy**: Deterministic PCRE scrubbing of internal RFC 1918 subnets, overlay hostnames, and client `User-Agent` strings.
* **Deliverability Doctor**: Built-in CLI diagnostic tool (`verify-dns.sh`) auditing MX, SPF, DKIM, DMARC, and FCrDNS PTR records via Cloudflare and Google DoH.
* **Zero-CVE Security Footprint**: Hardened Alpine 3.21 runtime dropping all capabilities (`cap_drop: [ALL]`), continuously audited via Aqua Security Trivy.
* **Native Telemetry**: Prometheus metrics exporter (`/metrics`), structured logs, and `/healthz`/`/readyz` Kubernetes/Compose probes.

---

## Quickstart

### 1. Initialize Environment & Keys
```bash
# Clone repository
git clone https://github.com/dropin-labs/drop-relay.git
cd drop-relay

# Configure environment
cp .env.example .env

# Generate 2048-bit RSA DKIM keypair and DNS TXT record
make dkim
# (Or using container: docker run --rm -v $(pwd)/keys:/etc/relay/dkim ghcr.io/dropin-labs/drop-relay:latest generate-dkim -domain example.com)
```

### 2. Verify Deliverability DNS
Audit and verify public DNS records via DNS-over-HTTPS before sending:
```bash
./scripts/verify-dns.sh example.com mail.example.com
```

### 3. Boot Container Stack
```bash
docker compose up -d
```

The relay is now listening on:
* **`http://localhost:8080`**: Ingestion REST API, `/healthz`, `/readyz`, and `/metrics`
* **`localhost:587`**: SMTP Submission (STARTTLS + Cyrus SASL)
* **`localhost:25`**: Inbound MX Listener (when public port 25 is mapped)

---

## Dispatch Examples

### HTTP REST API (`POST /v1/send`)
```bash
curl -X POST http://localhost:8080/v1/send \
  -H "Authorization: Bearer ${RELAY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "ops@example.com",
    "from_name": "Cloud Operations",
    "to": "devops@target.com",
    "subject": "Infrastructure Provisioned",
    "text": "All services operating normally.",
    "html": "<p>All services operating <strong>normally</strong>.</p>",
    "headers": {
      "X-Entity-ID": "tenant_1234"
    }
  }'
```

#### Scheduled & Delayed Dispatch
Schedule emails for deferred delivery using either a relative duration string (`delay`) or a future ISO 8601 / RFC 3339 timestamp (`send_at`):
```bash
# Relative delay (e.g., 30s, 15m, 2h)
curl -X POST http://localhost:8080/v1/send \
  -H "Authorization: Bearer ${RELAY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "ops@example.com",
    "to": "user@target.com",
    "subject": "Follow-up Reminder",
    "text": "Checking in after 1 hour.",
    "delay": "1h"
  }'

# Absolute RFC 3339 timestamp
curl -X POST http://localhost:8080/v1/send \
  -H "Authorization: Bearer ${RELAY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "ops@example.com",
    "to": "user@target.com",
    "subject": "Product Launch Announcement",
    "text": "We are officially live!",
    "send_at": "2026-10-28T09:00:00Z"
  }'
```

### SMTP Submission (Node.js / Python / Go)
```javascript
const nodemailer = require("nodemailer");

const transporter = nodemailer.createTransport({
  host: "127.0.0.1",
  port: 587,
  secure: false, // Upgrades opportunistically via STARTTLS
  auth: {
    user: process.env.SMTP_USER,
    pass: process.env.SMTP_PASS,
  },
});

await transporter.sendMail({
  from: '"Operations" <ops@example.com>',
  to: "recipient@domain.com",
  subject: "System Heartbeat",
  text: "drop-relay submission verified.",
});
```

---

## Inbound Webhook Pipe Bridge

When `INBOUND_WEBHOOK_URL` is configured, inbound mail delivered to Port 25 is piped to your application webhook:

```
[Inbound Email :25] ──► [mail-ingest.sh] ──(HMAC-SHA256)──► POST https://api.yourdomain.com/webhooks/mail
```

### Webhook Headers
| Header | Description | Example |
|:---|:---|:---|
| `Content-Type` | Raw MIME envelope | `message/rfc822` |
| `X-Relay-Signature` | HMAC-SHA256 of raw `.eml` body | `77522fbd377b0f...` |
| `X-Relay-Sender` | Envelope sender (`RFC 5321 MAIL FROM`) | `sender@external.com` |
| `X-Relay-Recipient` | Envelope recipient (`RFC 5321 RCPT TO`) | `inbox@example.com` |

### HTTP Status Code Mapping
* **`200..204`**: Successfully accepted (`EX_OK` = 0). Postfix deletes the spool message.
* **`404`**: User does not exist (`EX_NOUSER` = 67). Postfix emits an RFC 550 hard bounce.
* **`500` / Timeout**: Webhook unavailable (`EX_TEMPFAIL` = 75). Postfix retains the message and retries using exponential backoff.

---

## Configuration Matrix

| Environment Variable | Default | Required | Description |
|:---|:---|:---:|:---|
| `RELAY_DOMAIN` | `example.com` | **Yes** | Primary sending domain |
| `RELAY_HOSTNAME` | `mail.example.com` | **Yes** | FQDN for HELO/EHLO and MX identification |
| `RELAY_PORT_HTTP` | `8080` | No | HTTP Ingestion API listening port |
| `RELAY_API_KEY` | _(empty)_ | No | Bearer token for `POST /v1/send` authentication |
| `SMTP_USER` | `relay_user` | **Yes** | Cyrus SASL submission username (port 587) |
| `SMTP_PASS` | _(generated)_ | **Yes** | Cyrus SASL submission password |
| `DKIM_SELECTOR` | `relay` | No | DKIM DNS selector (published at `<selector>._domainkey`) |
| `DKIM_KEY_PATH` | `/etc/relay/dkim/relay.private` | No | File path to 2048-bit RSA private key |
| `BUFFER_TYPE` | `disk` | No | Spool buffer backend: `disk` (crash-durable), `memory`, or `redis` |
| `SPOOL_DIR` | `/var/spool/drop-relay` | No | Spool storage path on host/container (when `BUFFER_TYPE=disk`) |
| `BUFFER_CAPACITY` | `10000` | No | Maximum bounded queue capacity |
| `REDIS_URL` | `redis://127.0.0.1:6379` | No | Redis connection URI (when `BUFFER_TYPE=redis`) |
| `INBOUND_WEBHOOK_URL` | _(disabled)_ | No | HTTP webhook endpoint receiving piped inbound `.eml` payloads |
| `RELAY_INTEGRITY_SECRET`| _(generated)_ | No | Shared secret for `X-Relay-Signature` HMAC authentication |
| `UPSTREAM_SMART_HOST` | _(none)_ | No | Optional upstream smart-host (`[host]:port`) |
| `UPSTREAM_AUTH_USER` | _(none)_ | No | Upstream relay SASL username |
| `UPSTREAM_AUTH_PASS` | _(none)_ | No | Upstream relay SASL password |
| `MAX_MESSAGE_SIZE` | `26214400` | No | Maximum message size limit in bytes (default: 25MB) |

---

## Security & DevSecOps

`drop-relay` is engineered for mission-critical infrastructure with strict security gating:

* **Least-Privilege Isolation**: Runs with dropped Linux capabilities (`cap_drop: [ALL]`), retaining only `NET_BIND_SERVICE`, `SETUID`, `SETGID`, and `DAC_OVERRIDE`.
* **Automated CVE Scans**: Continuous vulnerability and secret scanning via **Aqua Security Trivy**. Builds strictly fail on `HIGH` or `CRITICAL` findings.
* **Header Scrubbing**: Outbound PCRE sanitization strips client IPs, Docker bridge subnets, and user agent metadata.

To run local security audits:
```bash
make scan        # Full security audit (filesystem + container image)
make scan-fs     # Filesystem, Go dependencies & secret scan
make scan-image  # Container image CVE scan
```

Review our full security policy and vulnerability disclosure procedures in **[SECURITY.md](SECURITY.md)**.

---

## Contributing

We welcome contributions adhering to our **Zero-Bloat** and **Config-First** standards. Review the **[Contributing Guide](CONTRIBUTING.md)** for development setup, testing requirements, and PR workflows.

```bash
make test       # Run unit test suite
make test-race  # Run unit tests with race detection
make test-e2e   # Run synthetic end-to-end integration test suite
make lint       # Run static analysis (go vet)
```

---

## License

Licensed under the **Apache 2.0 License**. See [LICENSE](LICENSE) for details.  
Maintained by **[Dropin Labs](https://github.com/dropin-labs)**.
