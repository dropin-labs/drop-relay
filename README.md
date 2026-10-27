# drop-relay

> High-throughput, self-contained mail transfer agent (MTA), DKIM signing engine, and bidirectional delivery proxy for modern container architectures.

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

---

## Quickstart

```bash
# 1. Clone repository and initialize environment
git clone https://github.com/dropin-labs/drop-relay.git && cd drop-relay
cp .env.example .env

# 2. Generate DKIM keys and export DNS records
docker run --rm -v $(pwd)/keys:/etc/opendkim/keys ghcr.io/dropin-labs/drop-relay:latest generate-dkim

# 3. Boot service stack
docker compose up -d
```

---

## Configuration Matrix

| Variable | Default | Required | Description |
|:---|:---|:---:|:---|
| `RELAY_DOMAIN` | `example.com` | Yes | Primary domain handled by the relay |
| `RELAY_HOSTNAME` | `mail.example.com` | Yes | FQDN for HELO/EHLO and MX identification |
| `SMTP_USER` | `relay_user` | Yes | Cyrus SASL submission username (port 587) |
| `SMTP_PASS` | _(generated)_ | Yes | Cyrus SASL submission password |
| `DKIM_SELECTOR` | `relay` | No | DKIM DNS selector (published at `<selector>._domainkey`) |
| `DKIM_KEY_PATH` | `/etc/opendkim/keys/relay.private` | No | Path to 2048-bit RSA private key |
| `INBOUND_WEBHOOK_URL` | _(disabled)_ | No | HTTP endpoint receiving piped inbound `.eml` payloads |
| `RELAY_INTEGRITY_SECRET`| _(random)_ | No | HMAC-SHA256 secret for `X-Relay-Signature` validation |
| `UPSTREAM_SMART_HOST` | _(none)_ | No | Upstream relay host (`[host]:port`) for smart-host routing |
| `UPSTREAM_AUTH_USER` | _(none)_ | No | Upstream relay SASL username |
| `UPSTREAM_AUTH_PASS` | _(none)_ | No | Upstream relay SASL password |
| `TLS_CERT_PATH` | _(ephemeral)_ | No | TLS certificate file path (PEM format) |
| `TLS_KEY_PATH` | _(ephemeral)_ | No | TLS private key file path (PEM format) |
| `MAX_MESSAGE_SIZE` | `26214400` | No | Maximum message size limit in bytes (default: 25MB) |

---

## Integration

### HTTP Dispatch (`curl`)
```bash
curl -X POST http://localhost:8080/v1/send \
  -H "Authorization: Bearer ${RELAY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "notifications@example.com",
    "to": "user@target.com",
    "subject": "System Alert",
    "text": "Task completed successfully.",
    "html": "<p>Task completed successfully.</p>"
  }'
```

### SMTP Dispatch (Nodemailer / Python / Go)
```javascript
const transporter = nodemailer.createTransport({
  host: "127.0.0.1",
  port: 587,
  secure: false, // upgrades via STARTTLS
  auth: { user: process.env.SMTP_USER, pass: process.env.SMTP_PASS }
});

await transporter.sendMail({
  from: '"Operations" <ops@example.com>',
  to: "recipient@domain.com",
  subject: "Ping",
  text: "drop-relay operational"
});
```

---

## Security & DevSecOps

`drop-relay` enforces continuous vulnerability, secret leak, and supply chain scanning via **Aqua Security Trivy**:

```bash
# Run full security audit (filesystem + container image)
make scan

# Run repository filesystem and Go dependency scan
make scan-fs

# Run container image CVE scan
make scan-image
```

All CI/CD runs and release pipelines enforce a strict gate on `HIGH` and `CRITICAL` findings, automatically uploading SARIF diagnostics to GitHub Security. See [SECURITY.md](SECURITY.md) for full policies.

---

## License & Contributing

Licensed under the Apache 2.0 License. See [LICENSE](LICENSE) for details.  
Contributions welcome: submit minimal PRs adhering to our strict Zero-Bloat and Config-First guidelines.  
Maintainer: [Dropin Labs](https://github.com/dropin-labs)
