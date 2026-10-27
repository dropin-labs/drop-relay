# drop-relay Architecture

High-throughput, self-contained mail transfer agent (MTA) and deliverability proxy engineered for containerized workloads.

---

## 1. System Pipeline

```
OUTBOUND PATH
[Client App] ──(HTTP POST :8080/v1/send)───┐
                                          ├──► [Ingestion Engine] ──► [Spool Buffer]
[Legacy App] ──(SMTP Submission :587)──────┘            │                (Memory / Redis)
                                                        │                         │
                                                        ▼                         ▼
                                              [Header Sanitizer]       [MTA Dispatch Worker]
                                              (Strip RFC 1918 IPs)                │
                                                        │                         ▼
                                                        └──────────────► [OpenDKIM Milter :8891]
                                                                         (relaxed/relaxed RSA-2048)
                                                                                  │
                                                                                  ▼
                                                                         [Postfix Queue Manager]
                                                                                  │
                                                  ┌───────────────────────────────┴───────────────────────────────┐
                                                  ▼                                                               ▼
                                       [Direct MX Delivery]                                            [Upstream Smart-Host]
                                       (Opportunistic TLS 1.3)                                         (Configured Relay)
                                                  │                                                               │
                                                  ▼                                                               ▼
                                       [Remote Recipient MTA]                                          [Third-Party Provider]

INBOUND PATH
[External MTA] ──► [Postfix MX :25] ──► [Milter Verify] ──► [Pre-Queue Policy] ──► [Pipe Transport] ──► [Webhook Bridge] ──► [Client API]
                   (Port 25)            (OpenDKIM / SPF)      (Static / HTTP / Redis)  (mail-ingest.sh)     (HMAC-SHA256)
```

---

## 2. Ingestion & Delivery Internals

### 2.1 Outbound Channels
1. **REST API (`POST /v1/send`)**: Accept JSON or multipart MIME payloads. Authenticates via `Authorization: Bearer <API_KEY>`. Normalizes CRLF line breaks (`\r\n`), injects compliant RFC 5322 headers (`Message-ID`, `Date`), and dispatches to the spool buffer.
2. **SMTP Submission (`:587`)**: RFC 6409 submission daemon. Requires explicit STARTTLS (`!SSLv2, !SSLv3, !TLSv1, !TLSv1.1`) and Cyrus SASL authentication. Non-authenticated client connections are rejected pre-handshake.

### 2.2 Header Sanitization & Privacy
Outbound messages pass through Postfix `header_checks` before transport. Internal container topologies and client identifiers are purged to prevent topology leakage:

```pcre
# Strip internal overlay network hops
/^\s*Received:.*(10\.\d+|172\.(1[6-9]|2\d|3[01])\.|192\.168\.|127\.0\.0\.1|dropin-network)/ IGNORE
# Strip client fingerprint headers
/^\s*(User-Agent|X-Mailer|X-Originating-IP):/ IGNORE
```

### 2.3 Cryptography & Signing
- **Milter Protocol**: Postfix interfaces with OpenDKIM daemon over internal loopback socket (`inet:127.0.0.1:8891`).
- **Canonicalization**: Enforces `relaxed/relaxed` for both header and body hashes (`bh=`), preventing DKIM invalidation caused by upstream MTA whitespace and QP transformations.
- **Oversigning**: `OversignHeaders From` prevents replay attacks that append secondary malicious `From` headers.

### 2.4 Inbound Ingestion Pipeline
1. Inbound connections land on TCP port 25. Postfix enforces `smtpd_recipient_restrictions` rejecting non-FQDN hostnames and invalid recipient syntax.
2. Verified messages are handed to the `drop-ingest` pipe transport (`/usr/local/bin/mail-ingest.sh`).
3. The bridge calculates an HMAC-SHA256 signature of the raw `.eml` payload using `RELAY_INTEGRITY_SECRET`.
4. Payload is delivered to `INBOUND_WEBHOOK_URL` as `Content-Type: message/rfc822` with headers:
   - `X-Relay-Signature`: Hex-encoded HMAC-SHA256.
   - `X-Relay-Sender`: Envelope sender (RFC 5321 MAIL FROM).
   - `X-Relay-Recipient`: Envelope recipient (RFC 5321 RCPT TO).
5. The pipe script maps HTTP response codes to POSIX `<sysexits.h>` statuses:
   - `200..204` -> `EX_OK` (0): Postfix discards spool entry; delivery successful.
   - `404` -> `EX_NOUSER` (67): Postfix generates hard bounce (550 User unknown).
   - Any other code or network timeout -> `EX_TEMPFAIL` (75): Message stays in Postfix spool and retries with backoff.

---

## 3. Design Decisions & Rationale

| Architecture Choice | Selection | Alternative Evaluated | Engineering Rationale |
|:---|:---|:---|:---|
| **MTA Core Engine** | Postfix 3.9+ | Haraka / Exim / Custom Go daemon | Battle-tested RFC conformance, unmatched queue isolation, robust kernel-level socket management, low memory footprint (<40MB). |
| **Milter System** | OpenDKIM | Rspamd / dkimpy | Dedicated, single-responsibility C daemon with native POSIX milter protocol support and minimal resource consumption. |
| **Submission Auth** | Cyrus SASL (`sasldb2`) | Dovecot / PAM / LDAP | Independent, lightweight credential database with zero external daemon runtime dependencies. |
| **Inbound Integration**| Pipe transport (`pipe`) | LMTP / Maildir polling | Instant event-driven HTTP delivery; leverages Postfix spool directly for temporary backoff when upstream API is degraded. |
| **Queue Resilience** | Hybrid (Spool + Redis) | Pure In-Memory FIFO | Guarantees zero data loss across container restarts; allows scale-out dispatch workers via Redis consumer groups. |

---

## 4. Deliverability, Reputation & Backoff Policy

### 4.1 Connection & Destination Limits
To maintain high IP reputation and prevent remote rate-limit blacklisting:

```ini
# Concurrent connections per remote domain
default_destination_concurrency_limit = 5
# Minimum interval between deliveries to same MX
default_destination_rate_delay = 1s
# Anvil anti-abuse: per-client connection limits
smtpd_client_connection_rate_limit = 30
smtpd_client_message_rate_limit = 100
```

### 4.2 Retry & Backoff Schedule
Failed delivery attempts follow a truncated exponential backoff schedule managed by Postfix `qmgr`:

$$\text{RetryInterval}(n) = \min\left(4000\text{s},\; 300\text{s} \times 2^{n-1}\right)$$

- **Initial retry**: 300 seconds (5 minutes).
- **Secondary retry**: 600 seconds (10 minutes).
- **Subsequent retries**: Exponential scaling up to 4,000 seconds (~66 minutes).
- **Maximal Queue Lifetime (`maximal_queue_lifetime`)**: 2 days. Undeliverable messages are returned to sender as a Delivery Status Notification (DSN) bounce.
- **Bounce Queue Lifetime (`bounce_queue_lifetime`)**: 1 day.

### 4.3 FCrDNS & Authentication Alignment Matrix
For 10/10 deliverability scoring (avoiding Gmail/Yahoo spam filtering):
- **HELO/EHLO**: Must match `RELAY_HOSTNAME` (e.g. `mail.example.com`).
- **Forward-Confirmed Reverse DNS (FCrDNS)**:
  $$\text{PTR}(\text{VPS\_IP}) \equiv \text{RELAY\_HOSTNAME} \quad \land \quad \text{A}(\text{RELAY\_HOSTNAME}) \equiv \text{VPS\_IP}$$
- **SPF Alignment**: Domain in `RFC 5321.MailFrom` must publish `v=spf1 ip4:<VPS_IP> ~all`.
- **DKIM Alignment**: Domain in `RFC 5322.From` must match the signing domain in DKIM tag `d=`.
- **DMARC Policy**: Published at `_dmarc.<domain>` enforcing SPF/DKIM alignment with `p=quarantine` or `p=reject`.
