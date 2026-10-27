# ==============================================================================
# DROP-RELAY — MULTI-STAGE DOCKERFILE
# ==============================================================================

# ── Stage 1: Go Compiler ──────────────────────────────────────────────────────
FROM golang:alpine AS builder

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Compile static binary
COPY . .
ARG VERSION=0.1.2
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /bin/relay ./cmd/relay

# ── Stage 2: Hardened Runtime ─────────────────────────────────────────────────
FROM alpine:3.21

# Install Postfix, OpenDKIM, Cyrus SASL, and runtime utilities
RUN apk add --no-cache \
    postfix \
    postfix-pcre \
    opendkim \
    opendkim-utils \
    cyrus-sasl \
    cyrus-sasl-login \
    cyrus-sasl-crammd5 \
    cyrus-sasl-digestmd5 \
    ca-certificates \
    bash \
    curl \
    gettext \
    openssl \
    netcat-openbsd

# Directory scaffold & permissions
RUN mkdir -p /var/spool/postfix \
    && mkdir -p /var/spool/drop-relay \
    && mkdir -p /var/run/opendkim \
    && mkdir -p /etc/opendkim/keys \
    && mkdir -p /etc/relay/dkim \
    && mkdir -p /etc/sasl2 \
    && mkdir -p /etc/drop-relay \
    && chown -R opendkim:opendkim /var/run/opendkim /etc/opendkim /etc/relay/dkim \
    && chmod 755 /var/run/opendkim \
    && chmod 750 /etc/opendkim/keys /etc/relay/dkim /var/spool/drop-relay

# Copy configuration blueprints and runtime templates
COPY config/postfix/main.cf.tpl /etc/postfix/main.cf.tpl
COPY config/postfix/master.cf.tpl /etc/postfix/master.cf.tpl
COPY config/postfix/header_checks.pcre /etc/postfix/header_checks.pcre
COPY config/opendkim.conf.tpl /etc/opendkim/opendkim.conf

# Copy compiled binary and supervisor scripts
COPY --from=builder /bin/relay /usr/local/bin/relay
COPY scripts/mail-entrypoint.sh /usr/local/bin/mail-entrypoint.sh
COPY scripts/mail-ingest.sh /usr/local/bin/mail-ingest.sh

RUN chmod +x /usr/local/bin/mail-entrypoint.sh /usr/local/bin/mail-ingest.sh /usr/local/bin/relay

# Port 25 (Inbound MX), Port 587 (SMTP Submission), Port 8080 (Ingestion HTTP API)
EXPOSE 25 587 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD nc -z 127.0.0.1 25 && nc -z 127.0.0.1 587 && wget -qO- http://127.0.0.1:8080/healthz || exit 1

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/mail-entrypoint.sh"]
