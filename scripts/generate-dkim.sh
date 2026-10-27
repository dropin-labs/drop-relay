#!/usr/bin/env sh
# ==============================================================================
# DROP-RELAY — AUTOMATED DKIM KEY GENERATOR & DNS EXPORTER
# ==============================================================================
# Generates 2048-bit RSA keypair and formats the DNS TXT record for publication.
# Uses `relay generate-dkim` when available, falling back to openssl directly.
# ==============================================================================

set -eu

DOMAIN=""
SELECTOR="default"
OUT_PATH=""

while [ $# -gt 0 ]; do
    case "$1" in
        --domain|-d)
            DOMAIN="$2"; shift 2 ;;
        --selector|-s)
            SELECTOR="$2"; shift 2 ;;
        --out|-o)
            OUT_PATH="$2"; shift 2 ;;
        --help)
            echo "Usage: $0 --domain <domain> [--selector <selector>] [--out <private_key_path>]"
            exit 0 ;;
        *)
            if [ -z "$DOMAIN" ]; then
                DOMAIN="$1"
            elif [ -z "$SELECTOR" ]; then
                SELECTOR="$1"
            fi
            shift ;;
    esac
done

if [ -z "$DOMAIN" ]; then
    echo "ERROR: --domain is required."
    echo "Usage: $0 --domain <domain> [--selector <selector>] [--out <private_key_path>]"
    exit 1
fi

# Default output directory: ./keys or /etc/relay/dkim
if [ -z "$OUT_PATH" ]; then
    if [ -w "/etc/relay/dkim" ]; then
        OUT_PATH="/etc/relay/dkim/${DOMAIN}.private"
    elif [ -w "/etc/opendkim/keys" ]; then
        OUT_PATH="/etc/opendkim/keys/${SELECTOR}.${DOMAIN}.private"
    else
        mkdir -p "./keys"
        OUT_PATH="./keys/${DOMAIN}.private"
    fi
fi

# Ensure parent directory exists
mkdir -p "$(dirname "$OUT_PATH")"

# 1. Check if relay binary is available
RELAY_BIN=""
if [ -x "./bin/relay" ]; then
    RELAY_BIN="./bin/relay"
elif command -v relay >/dev/null 2>&1; then
    RELAY_BIN="relay"
fi

if [ -n "$RELAY_BIN" ]; then
    "$RELAY_BIN" generate-dkim -domain "$DOMAIN" -selector "$SELECTOR" -out "$OUT_PATH"
    exit 0
fi

# 2. Fallback using openssl
echo "[INFO] Using openssl fallback generator..."
openssl genrsa -out "$OUT_PATH" 2048 2>/dev/null
chmod 400 "$OUT_PATH"

PUB_B64=$(openssl rsa -in "$OUT_PATH" -pubout -outform PEM 2>/dev/null | grep -v '^-' | tr -d '\n')
DNS_RECORD="v=DKIM1; k=rsa; p=${PUB_B64}"

# Format 250-character chunks for DNS zone files
CHUNKED_RECORD=$(echo "$DNS_RECORD" | fold -w 250 | sed 's/^/"/' | sed 's/$/"/' | tr '\n' ' ' | sed 's/ $//')

echo ""
echo "=== DKIM Key Generation Successful ==="
echo "Private Key Path: ${OUT_PATH}"
echo "Selector:         ${SELECTOR}"
echo "Domain:           ${DOMAIN}"
echo ""
echo "--- DNS TXT Record (Publish at ${SELECTOR}._domainkey.${DOMAIN}) ---"
echo "Host/Name: ${SELECTOR}._domainkey"
echo "Type:      TXT"
echo "Value (Single Line):"
echo "  ${DNS_RECORD}"
echo ""
echo "Value (RFC 4408 Split/Chunked Format for BIND/Cloudflare):"
echo "  ${CHUNKED_RECORD}"
echo ""
echo "Raw Base64 Public Key: ${PUB_B64}"
echo ""
