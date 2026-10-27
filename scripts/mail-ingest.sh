#!/bin/bash
# ==============================================================================
# DROP-RELAY — INBOUND WEBHOOK PIPE BRIDGE
# ==============================================================================
# Invoked by Postfix pipe(8) transport for inbound emails on Port 25.
# Bridges incoming raw RFC 822 (.eml) payloads to client HTTP webhook.
# Exit codes adhere to <sysexits.h>:
#   0  = EX_OK (Delivered; Postfix discards spool entry)
#   67 = EX_NOUSER (Permanent 550 bounce; recipient not found)
#   75 = EX_TEMPFAIL (Temporary failure; Postfix retries with exponential backoff)

set -uo pipefail

SENDER="${1:-}"
RECIPIENT="${2:-}"

CONFIG_ENV="${DROP_RELAY_ENV:-/etc/drop-relay/inbound.env}"
if [[ -f "$CONFIG_ENV" ]]; then
    # shellcheck disable=SC1091
    source "$CONFIG_ENV"
fi

# If inbound webhook is unconfigured, accept without forwarding
if [[ -z "${INBOUND_WEBHOOK_URL:-}" ]]; then
    cat > /dev/null
    exit 0
fi

TMP_EML="/tmp/relay_inbound_$$_${RANDOM}.eml"
trap 'rm -f "$TMP_EML"' EXIT

cat > "$TMP_EML"

if [[ ! -s "$TMP_EML" ]]; then
    exit 0
fi

# Calculate HMAC-SHA256 signature
SIG=$(openssl dgst -sha256 -hmac "${RELAY_INTEGRITY_SECRET:-}" -hex "$TMP_EML" | awk '{print $NF}')

# Dispatch to client webhook
HTTP_CODE=$(curl -sS -o /dev/null -w "%{http_code}" \
    --max-time 20 \
    -X POST "${INBOUND_WEBHOOK_URL}" \
    -H "Content-Type: message/rfc822" \
    -H "X-Relay-Signature: ${SIG}" \
    -H "x-dropin-mail-signature: ${SIG}" \
    -H "X-Relay-Recipient: ${RECIPIENT}" \
    -H "x-dropin-mail-recipient: ${RECIPIENT}" \
    -H "X-Relay-Sender: ${SENDER}" \
    -H "x-dropin-mail-sender: ${SENDER}" \
    --data-binary "@${TMP_EML}" 2>/dev/null || echo "000")

case "$HTTP_CODE" in
    200|201|202|204)
        exit 0
        ;;
    404)
        exit 67
        ;;
    *)
        exit 75
        ;;
esac
