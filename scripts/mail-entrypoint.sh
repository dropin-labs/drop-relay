#!/bin/bash
# ==============================================================================
# DROP-RELAY — CONTAINER SUPERVISION & RUNTIME ENTRYPOINT
# ==============================================================================
set -euo pipefail

# Mirror Postfix syslog to container stdout
mkdir -p /var/log
touch /var/log/mail.log
syslogd -O /var/log/mail.log 2>/dev/null || true
tail -n 0 -F /var/log/mail.log 2>/dev/null &

echo "======================================================================"
echo "                   DROP-RELAY RUNTIME SUPERVISOR                     "
echo "======================================================================"

# ── 1. Resolve Identity & Network Context ─────────────────────────────────────
export RELAY_DOMAIN="${RELAY_DOMAIN:-example.com}"
export RELAY_HOSTNAME="${RELAY_HOSTNAME:-mail.${RELAY_DOMAIN}}"
export RELAY_PORT_HTTP="${RELAY_PORT_HTTP:-8080}"
export SMTP_USER="${SMTP_USER:-relay_user}"
export DKIM_SELECTOR="${DKIM_SELECTOR:-relay}"
export MAX_MESSAGE_SIZE="${MAX_MESSAGE_SIZE:-26214400}"

# ── 2. Resolve Credentials & Secrets ──────────────────────────────────────────
if [[ -z "${SMTP_PASS:-}" ]]; then
    if [[ -f "/run/secrets/smtp_pass" ]]; then
        SMTP_PASS=$(tr -d '\n\r' < /run/secrets/smtp_pass)
    else
        SMTP_PASS=$(openssl rand -hex 16)
        echo "[INFO] Generated ephemeral submission password: ${SMTP_PASS}"
    fi
fi
export SMTP_PASS

if [[ -z "${RELAY_INTEGRITY_SECRET:-}" ]]; then
    if [[ -f "/run/secrets/integrity_secret" ]]; then
        RELAY_INTEGRITY_SECRET=$(tr -d '\n\r' < /run/secrets/integrity_secret)
    else
        RELAY_INTEGRITY_SECRET=$(openssl rand -hex 32)
    fi
fi
export RELAY_INTEGRITY_SECRET

# ── 3. Resolve or Generate TLS Certificates ───────────────────────────────────
export TLS_CERT_PATH="${TLS_CERT_PATH:-}"
export TLS_KEY_PATH="${TLS_KEY_PATH:-}"

if [[ -z "$TLS_CERT_PATH" ]] || [[ ! -f "$TLS_CERT_PATH" ]] || [[ -z "$TLS_KEY_PATH" ]] || [[ ! -f "$TLS_KEY_PATH" ]]; then
    echo "[INFO] Production TLS certificate not found. Generating ephemeral self-signed certificate..."
    mkdir -p /etc/postfix/ssl
    TLS_CERT_PATH="/etc/postfix/ssl/ephemeral.crt"
    TLS_KEY_PATH="/etc/postfix/ssl/ephemeral.key"
    if [[ ! -f "$TLS_CERT_PATH" ]]; then
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout "$TLS_KEY_PATH" \
            -out "$TLS_CERT_PATH" \
            -subj "/CN=${RELAY_HOSTNAME}/O=drop-relay" 2>/dev/null
        chmod 600 "$TLS_KEY_PATH"
        chmod 644 "$TLS_CERT_PATH"
    fi
fi
export TLS_CERT_PATH
export TLS_KEY_PATH

# ── 4. Configure Cyrus SASL Authentication ────────────────────────────────────
echo "[INFO] Initializing Cyrus SASL authentication database..."
mkdir -p /etc/sasl2
cat > /etc/sasl2/smtpd.conf <<'EOF'
pwcheck_method: auxprop
auxprop_plugin: sasldb
mech_list: PLAIN LOGIN CRAM-MD5 DIGEST-MD5
log_level: 3
EOF

echo "$SMTP_PASS" | saslpasswd2 -p -c -u "$RELAY_DOMAIN" -a smtpd "$SMTP_USER"
echo "$SMTP_PASS" | saslpasswd2 -p -c -u "$RELAY_HOSTNAME" -a smtpd "$SMTP_USER"
echo "$SMTP_PASS" | saslpasswd2 -p -c -a smtpd "$SMTP_USER"

chown root:postfix /etc/sasl2/sasldb2
chmod 640 /etc/sasl2/sasldb2

# ── 5. OpenDKIM Cryptographic Key & Multi-Tenant Table Hydration ──────────────
echo "[INFO] Configuring OpenDKIM milter tables..."
mkdir -p /var/run/opendkim /etc/opendkim/keys /etc/relay/dkim
chown -R opendkim:opendkim /var/run/opendkim /etc/opendkim /etc/relay/dkim
chmod 750 /etc/opendkim/keys /etc/relay/dkim

# Ensure primary domain key exists
PRIMARY_KEY="/etc/opendkim/keys/${DKIM_SELECTOR}.private"
if [[ ! -f "$PRIMARY_KEY" ]] && [[ ! -f "/etc/relay/dkim/${RELAY_DOMAIN}.private" ]]; then
    echo "[INFO] DKIM private key not found for ${RELAY_DOMAIN}. Generating 2048-bit RSA key..."
    /usr/local/bin/relay generate-dkim -domain "$RELAY_DOMAIN" -selector "$DKIM_SELECTOR" -out "$PRIMARY_KEY"
fi

# Initialize tables
true > /etc/opendkim/KeyTable
true > /etc/opendkim/SigningTable
cat > /etc/opendkim/TrustedHosts <<EOF
127.0.0.1
::1
localhost
10.0.0.0/8
172.16.0.0/12
192.168.0.0/16
${RELAY_DOMAIN}
*.${RELAY_DOMAIN}
EOF

# Multi-tenant scanner: inspect both /etc/opendkim/keys and /etc/relay/dkim
REGISTERED_DOMAINS=""
for KEY_PATH in /etc/opendkim/keys/*.private /etc/relay/dkim/*.private; do
    [[ -f "$KEY_PATH" ]] || continue

    FILENAME=$(basename "$KEY_PATH" .private)
    CUR_SEL="$DKIM_SELECTOR"
    CUR_DOM="$RELAY_DOMAIN"

    if [[ "$FILENAME" == *"."* ]]; then
        # Format: selector.domain.com.private or domain.com.private
        DOT_COUNT=$(echo "$FILENAME" | tr -cd '.' | wc -c)
        if [[ "$DOT_COUNT" -gt 1 ]] && [[ "$FILENAME" != "mail."* ]] && [[ "$FILENAME" != "www."* ]]; then
            CUR_SEL=$(echo "$FILENAME" | cut -d'.' -f1)
            CUR_DOM=$(echo "$FILENAME" | cut -d'.' -f2-)
        else
            CUR_DOM="$FILENAME"
        fi
    elif [[ "$FILENAME" != "$DKIM_SELECTOR" ]]; then
        CUR_DOM="$FILENAME"
    fi

    chown opendkim:opendkim "$KEY_PATH"
    chmod 400 "$KEY_PATH"

    RECORD_TAG="${CUR_SEL}._domainkey.${CUR_DOM}"
    echo "${RECORD_TAG} ${CUR_DOM}:${CUR_SEL}:${KEY_PATH}" >> /etc/opendkim/KeyTable
    echo "*@${CUR_DOM} ${RECORD_TAG}" >> /etc/opendkim/SigningTable
    echo "*@*.${CUR_DOM} ${RECORD_TAG}" >> /etc/opendkim/SigningTable

    echo "${CUR_DOM}" >> /etc/opendkim/TrustedHosts
    echo "*.${CUR_DOM}" >> /etc/opendkim/TrustedHosts

    REGISTERED_DOMAINS="${REGISTERED_DOMAINS} ${CUR_DOM}(${CUR_SEL})"
done

echo "[INFO] OpenDKIM signing registered domains:${REGISTERED_DOMAINS}"
chown opendkim:opendkim /etc/opendkim/*
chmod 640 /etc/opendkim/*

# ── 6. Inbound Webhook Configuration ──────────────────────────────────────────
mkdir -p /etc/drop-relay
cat > /etc/drop-relay/inbound.env <<EOF
INBOUND_WEBHOOK_URL="${INBOUND_WEBHOOK_URL:-}"
RELAY_INTEGRITY_SECRET="${RELAY_INTEGRITY_SECRET}"
EOF
chown root:nobody /etc/drop-relay/inbound.env
chmod 640 /etc/drop-relay/inbound.env

# ── 7. Postfix Configuration Hydration ────────────────────────────────────────
echo "[INFO] Hydrating Postfix configuration templates..."
envsubst '${RELAY_HOSTNAME} ${RELAY_DOMAIN} ${TLS_CERT_PATH} ${TLS_KEY_PATH} ${MAX_MESSAGE_SIZE}' \
    < /etc/postfix/main.cf.tpl > /etc/postfix/main.cf
cp /etc/postfix/master.cf.tpl /etc/postfix/master.cf

# Optional Upstream Smart-Host Relay
if [[ -n "${UPSTREAM_SMART_HOST:-}" ]]; then
    echo "[INFO] Configuring upstream smart-host relay: [${UPSTREAM_SMART_HOST}]..."
    DB_TYPE=$(postconf -h default_database_type 2>/dev/null || echo "lmdb")
    cat >> /etc/postfix/main.cf <<EOF

# Upstream Smart Host Configuration
relayhost = [${UPSTREAM_SMART_HOST}]
smtp_sasl_auth_enable = yes
smtp_sasl_password_maps = ${DB_TYPE}:/etc/postfix/sasl_passwd
smtp_sasl_security_options = noanonymous
smtp_tls_security_level = encrypt
EOF

    if [[ -n "${UPSTREAM_AUTH_USER:-}" ]] && [[ -n "${UPSTREAM_AUTH_PASS:-}" ]]; then
        echo "[${UPSTREAM_SMART_HOST}] ${UPSTREAM_AUTH_USER}:${UPSTREAM_AUTH_PASS}" > /etc/postfix/sasl_passwd
        chmod 600 /etc/postfix/sasl_passwd
        postmap "${DB_TYPE}:/etc/postfix/sasl_passwd"
    fi
fi

newaliases 2>/dev/null || true
postfix set-permissions 2>/dev/null || true

# Re-assert permissions
chown root:nobody /etc/drop-relay/inbound.env /usr/local/bin/mail-ingest.sh
chmod 640 /etc/drop-relay/inbound.env
chmod 750 /usr/local/bin/mail-ingest.sh

# ── 8. Process Supervision ────────────────────────────────────────────────────
echo "[INFO] Starting OpenDKIM daemon..."
opendkim -x /etc/opendkim/opendkim.conf

echo "[INFO] Starting Postfix MTA master..."
postfix start

echo "[INFO] Starting drop-relay Ingestion & Buffer Daemon..."
/usr/local/bin/relay &
RELAY_PID=$!

# POSIX Signal Trap for Graceful Shutdown
shutdown_handler() {
    echo ""
    echo "[INFO] Received termination signal. Initiating graceful shutdown..."
    if kill -0 "$RELAY_PID" 2>/dev/null; then
        kill -TERM "$RELAY_PID"
        wait "$RELAY_PID" 2>/dev/null || true
    fi
    postfix stop 2>/dev/null || true
    pkill -TERM opendkim 2>/dev/null || true
    pkill -TERM syslogd 2>/dev/null || true
    echo "[INFO] All services stopped cleanly."
    exit 0
}

trap shutdown_handler SIGTERM SIGINT

# Keep entrypoint alive supervising background relay process
wait "$RELAY_PID"
