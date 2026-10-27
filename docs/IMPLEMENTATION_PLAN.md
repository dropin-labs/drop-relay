# drop-relay Implementation Plan

A phased roadmap for extracting, generalizing, and deploying Dropin Labs' sovereign email relay infrastructure as a standalone, production-grade open-source system.

---

## 1. Scope & Architectural Boundaries

### In Scope
- **Dual-Interface Ingestion**: High-throughput HTTP API (`POST /v1/send`) and RFC 5322/6409 SMTP submission daemon (`:587`).
- **Inbound MX Gateway**: Inbound RFC 5321 listener (`:25`) with pre-queue policy validation, DKIM verification, and generic webhook dispatch (`POST <WEBHOOK_URL>`).
- **Cryptographic Engine**: Automated 2048/4096-bit RSA DKIM keygen, dynamic multi-tenant domain signing, canonicalization (`relaxed/relaxed`), and DNS TXT export.
- **Queueing & Backpressure**: Pluggable storage buffer (in-memory FIFO, Redis streams, or LMDB/disk spool) with exponential backoff and Dead-Letter Queue (DLQ).
- **Transport & Delivery**: Direct MX routing via DNS MX resolution, opportunistic/enforced DANE/STARTTLS, connection pooling, and optional upstream smart-host fallback.
- **Header Hygiene**: Deterministic stripping of private subnet IPs (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.1`), container hostnames, and client metadata.
- **Observability**: Prometheus metrics exporter (`/metrics`), structured JSON stdout logs, and deliverability health check engine.

### Out of Scope
- User mailbox management (IMAP/POP3 servers, webmail interfaces).
- Template authoring and rendering engines (must be handled upstream by clients).
- End-user identity/auth federation (OIDC, OAuth2); drop-relay uses API keys and SMTP SASL credentials.
- Marketing list management, unsubscribe pages, or campaign management.

---

## 2. Target Repository Topology

```
drop-relay/
├── cmd/
│   └── relay/               # Entrypoint binary (CLI / daemon launcher)
├── config/
│   ├── config.example.yaml  # Unified file configuration
│   ├── opendkim.conf.tpl    # OpenDKIM runtime template
│   └── postfix/             # Postfix configuration blueprints
│       ├── main.cf.tpl
│       ├── master.cf.tpl
│       └── header_checks.pcre
├── docker/
│   ├── Dockerfile           # Minimal multi-stage production image
│   └── docker-compose.yml   # Standalone quickstart stack
├── docs/
│   ├── ARCHITECTURE.md      # Protocol internals and lifecycle specs
│   └── IMPLEMENTATION_PLAN.md # This extraction plan
├── internal/
│   ├── buffer/              # In-memory & Redis queue backends
│   ├── crypto/              # DKIM key generation, rotation, signing
│   ├── deliver/             # Direct MX pool, DNS resolver, smart-host router
│   ├── dkim/                # Milter / signing bridge
│   ├── http/                # Ingestion API, webhook dispatcher, health probes
│   ├── metrics/             # Prometheus collectors (counters, histograms)
│   └── smtp/                # Inbound MX listener and submission server
├── scripts/
│   ├── generate-dkim.sh     # Standalone CLI key generator & DNS helper
│   ├── verify-dns.sh        # Live SPF/DKIM/DMARC/MX/PTR doctor
│   └── test-e2e.sh          # Synthetic SMTP & HTTP end-to-end integration test
├── .env.example             # Exhaustive environment variable reference
├── LICENSE                  # Apache 2.0
├── Makefile                 # Build, test, lint, and run targets
└── README.md                # 60-second entrypoint
```

---

## 3. Decoupling & Generalization Strategy

| Dimension | MVP Coupling (`dropin-mvp`) | OSS Generalization (`drop-relay`) |
|:---|:---|:---|
| **Domain Binding** | Hardcoded `dropin.rw`, `prod.dropin.rw`, `staging.dropin.rw` | Dynamic via `RELAY_DOMAINS` CSV list or regex pattern matching |
| **Recipient Validation** | Direct PostgreSQL query to `mail_accounts` table | Pluggable: Local static list, Redis set, HTTP webhook probe, or permissive relay mode |
| **Inbound Webhook** | Proprietary endpoint `POST /api/internal/mail/inbound` with `x-dropin-mail-signature` | Standard RFC 822 or parsed JSON payload sent to `INBOUND_WEBHOOK_URL` with HMAC header `X-Relay-Signature` |
| **DKIM Storage** | Static path `/etc/opendkim/keys/dropin.private` | Dynamic multi-domain key store directory `/etc/relay/dkim/{domain}.private` with auto-generation fallback |
| **Header Stripping** | Hardcoded regex matching `dropin-network` | Configurable PCRE rule list (`HEADER_STRIP_REGEX`) sanitizing all RFC 1918 subnets and internal hops |
| **Network Security** | Docker Compose service `dropin-mail` in private subnet | Standalone container with isolated unprivileged UID, configurable port mappings, and read-only root FS |

---

## 4. Step-by-Step Build Roadmap

### Phase 2.1: Transport Core & Container Isolation
- [ ] Build minimal hardened container based on Alpine Linux containing Postfix 3.9+ and OpenDKIM.
- [ ] Implement parameter hydration in startup script (`mail-entrypoint.sh`) using environment variables and template substitution.
- [ ] Implement port isolation: Port 25 for public MX (opportunistic TLS, no auth), Port 587 for internal submission (mandatory TLS, SASL auth).
- [ ] Implement configurable upstream smart-host relay with optional SASL credentials (`RELAY_HOST`, `RELAY_PORT`, `RELAY_USER`, `RELAY_PASS`).
- [ ] Apply Linux capabilities drop (`cap_drop: [ALL]`, adding only `NET_BIND_SERVICE`, `SETUID`, `SETGID`, `DAC_OVERRIDE`).

### Phase 2.2: Ingestion & Buffer Layer
- [ ] Create HTTP REST daemon exposing `POST /v1/send` with Bearer token authentication.
- [ ] Implement RFC 5322 payload validation and HTML document normalization (auto-wrapping non-conforming snippets with doctype).
- [ ] Implement dual-mode spool buffer:
  - Mode A: In-memory circular buffer with configurable capacity for single-container setups.
  - Mode B: Redis Streams client with consumer groups, ACK tracking, and retry backpressure.
- [ ] Implement connection pooling for downstream MTA submission (`maxConnections: 10`, `maxMessages: 100`).

### Phase 2.3: Cryptography & Deliverability Engine
- [ ] Build `scripts/generate-dkim.sh` to produce 2048-bit RSA keys and print ready-to-paste DNS TXT records.
- [ ] Wire automated startup key generation: if private key is absent, generate ephemeral keypair and log formatted DNS record to stdout.
- [ ] Configure OpenDKIM milter with `relaxed/relaxed` canonicalization and `OversignHeaders From` to guarantee verification across UTF-8 MTA hops.
- [ ] Implement generic inbound webhook bridge: pipe incoming raw `.eml` from Postfix to configured `INBOUND_WEBHOOK_URL` with HMAC-SHA256 signature.
- [ ] Build standalone `scripts/verify-dns.sh` CLI diagnostic tool to audit A, MX, SPF, DKIM, DMARC, and PTR alignment via DoH (DNS-over-HTTPS).

### Phase 2.4: Observability, Metrics & Hardening
- [ ] Implement Prometheus metrics exporter (`/metrics`):
  - `relay_messages_ingested_total{channel="http|smtp"}`
  - `relay_messages_delivered_total{domain, status}`
  - `relay_messages_failed_total{domain, code}`
  - `relay_queue_depth{queue="spool|dlq"}`
  - `relay_delivery_latency_seconds` (histogram)
- [ ] Implement structured JSON logging to stdout with sensitive header/payload redaction.
- [ ] Implement container health check script verifying Postfix status, port listeners, and OpenDKIM milter socket.
- [ ] Package multi-arch Docker image (`linux/amd64`, `linux/arm64`) with automated GitHub Actions workflow.
