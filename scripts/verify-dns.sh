#!/usr/bin/env sh
# ==============================================================================
# DROP-RELAY — DELIVERABILITY DIAGNOSTICS DOCTOR
# ==============================================================================
# Audits DNS records (MX, SPF, DKIM, DMARC, PTR) via DNS-over-HTTPS (DoH)
# to evaluate external deliverability alignment without resolver cache pollution.
# ==============================================================================

set -eu

# ANSI Colors
ESC=$(printf '\033')
RED="${ESC}[0;31m"
GREEN="${ESC}[0;32m"
YELLOW="${ESC}[1;33m"
BLUE="${ESC}[0;34m"
CYAN="${ESC}[0;36m"
BOLD="${ESC}[1m"
NC="${ESC}[0m"

DOMAIN=""
SELECTOR="relay"
MAIL_HOSTNAME=""
VPS_IP=""

# Parse arguments
while [ $# -gt 0 ]; do
    case "$1" in
        --domain|-d)
            DOMAIN="$2"; shift 2 ;;
        --selector|-s)
            SELECTOR="$2"; shift 2 ;;
        --hostname|-h)
            MAIL_HOSTNAME="$2"; shift 2 ;;
        --ip|-i)
            VPS_IP="$2"; shift 2 ;;
        --help)
            echo "Usage: $0 [--domain <domain>] [--selector <selector>] [--hostname <mail_host>] [--ip <public_ip>]"
            exit 0 ;;
        *)
            if [ -z "$DOMAIN" ]; then
                DOMAIN="$1"
            elif [ -z "$VPS_IP" ]; then
                VPS_IP="$1"
            fi
            shift ;;
    esac
done

DOMAIN="${DOMAIN:-${RELAY_DOMAIN:-example.com}}"
MAIL_HOSTNAME="${MAIL_HOSTNAME:-${RELAY_HOSTNAME:-mail.${DOMAIN}}}"
VPS_IP="${VPS_IP:-${PUBLIC_IP:-}}"

echo "${CYAN}======================================================================${NC}"
echo "${BOLD}${CYAN}            DROP-RELAY DELIVERABILITY DIAGNOSTIC DOCTOR               ${NC}"
echo "${CYAN}======================================================================${NC}"
printf " Target Domain:   ${BOLD}%s${NC}\n" "$DOMAIN"
printf " DKIM Selector:   ${BOLD}%s${NC}\n" "$SELECTOR"
printf " Mail Hostname:   ${BOLD}%s${NC}\n" "$MAIL_HOSTNAME"
if [ -n "$VPS_IP" ]; then
    printf " VPS Public IP:   ${BOLD}%s${NC}\n" "$VPS_IP"
fi
echo "${CYAN}======================================================================${NC}"
echo ""

TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0
WARNINGS=0

# DoH Query Helper using Google / Cloudflare
doh_query() {
    name="$1"
    type="$2"
    curl -sS -H "accept: application/dns-json" \
        --max-time 10 \
        "https://dns.google/resolve?name=${name}&type=${type}" 2>/dev/null || echo "{}"
}

pass() {
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
    printf "  ${GREEN}[PASS]${NC} %s\n" "$1"
    if [ $# -gt 1 ]; then
        printf "         ${CYAN}Value:${NC} %s\n" "$2"
    fi
}

warn() {
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    WARNINGS=$((WARNINGS + 1))
    printf "  ${YELLOW}[WARN]${NC} %s\n" "$1"
    if [ $# -gt 1 ]; then
        printf "         ${CYAN}Notice:${NC} %s\n" "$2"
    fi
}

fail() {
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    FAILED_CHECKS=$((FAILED_CHECKS + 1))
    printf "  ${RED}[FAIL]${NC} %s\n" "$1"
    if [ $# -gt 1 ]; then
        printf "         ${RED}Fix:${NC} %s\n" "$2"
    fi
}

# ── 1. MX Record Check ────────────────────────────────────────────────────────
echo "${BLUE}[1/5] Auditing Inbound MX Records...${NC}"
MX_JSON=$(doh_query "$DOMAIN" "MX")
MX_ANSWERS=$(echo "$MX_JSON" | jq -r '.Answer[]? | select(.type == 15) | .data' 2>/dev/null || true)

if [ -n "$MX_ANSWERS" ]; then
    MATCHED=0
    for entry in $MX_ANSWERS; do
        target=$(echo "$entry" | awk '{print $2}' | sed 's/\.$//')
        if [ "$target" = "$MAIL_HOSTNAME" ]; then
            MATCHED=1
        fi
    done

    if [ "$MATCHED" -eq 1 ]; then
        pass "MX points to mail hostname (${MAIL_HOSTNAME})" "$MX_ANSWERS"
    else
        warn "MX record found, but does not point to ${MAIL_HOSTNAME}" "Current: ${MX_ANSWERS} (Expected: 10 ${MAIL_HOSTNAME}.)"
    fi
else
    fail "No MX records found for ${DOMAIN}" "Add DNS Record: ${DOMAIN}. IN MX 10 ${MAIL_HOSTNAME}."
fi

echo ""

# ── 2. SPF Record Check ───────────────────────────────────────────────────────
echo "${BLUE}[2/5] Auditing SPF Authorization...${NC}"
TXT_JSON=$(doh_query "$DOMAIN" "TXT")
SPF_RECORDS=$(echo "$TXT_JSON" | jq -r '.Answer[]? | select(.type == 16) | .data' 2>/dev/null | tr -d '"' | grep -i '^v=spf1' || true)

SPF_COUNT=$(echo "$SPF_RECORDS" | grep -c '^v=spf1' || true)

if [ "$SPF_COUNT" -eq 1 ]; then
    case "$SPF_RECORDS" in
        *"-all")
            pass "SPF configured with strict hardfail (-all)" "$SPF_RECORDS" ;;
        *"~all")
            pass "SPF configured with softfail (~all)" "$SPF_RECORDS" ;;
        *"+all"|*"?all")
            warn "SPF is permissive (${SPF_RECORDS})" "Change +all or ?all to ~all or -all to prevent spoofing" ;;
        *)
            warn "SPF record lacks terminating all mechanism" "$SPF_RECORDS" ;;
    esac
elif [ "$SPF_COUNT" -gt 1 ]; then
    fail "Multiple SPF records detected (${SPF_COUNT})" "RFC 7208 mandates exactly ONE SPF record per domain. Merge them into one."
else
    fail "SPF record missing for ${DOMAIN}" "Add DNS TXT Record: ${DOMAIN}. IN TXT \"v=spf1 mx ~all\""
fi

echo ""

# ── 3. DKIM Key Alignment ─────────────────────────────────────────────────────
echo "${BLUE}[3/5] Auditing DKIM Public Key...${NC}"
DKIM_FQDN="${SELECTOR}._domainkey.${DOMAIN}"
DKIM_JSON=$(doh_query "$DKIM_FQDN" "TXT")
DKIM_RECORD=$(echo "$DKIM_JSON" | jq -r '.Answer[]? | select(.type == 16) | .data' 2>/dev/null | tr -d '"' | tr -d ' ' || true)

if [ -n "$DKIM_RECORD" ] && echo "$DKIM_RECORD" | grep -qi 'v=DKIM1'; then
    if echo "$DKIM_RECORD" | grep -qi 'p='; then
        KEY_PAYLOAD=$(echo "$DKIM_RECORD" | sed -n 's/.*p=\([A-Za-z0-9+/=]*\).*/\1/p')
        KEY_LEN=${#KEY_PAYLOAD}
        if [ "$KEY_LEN" -ge 350 ]; then
            pass "2048-bit RSA DKIM key published at ${DKIM_FQDN}" "Key length: ~2048 bits"
        elif [ "$KEY_LEN" -ge 180 ]; then
            warn "1024-bit RSA DKIM key published at ${DKIM_FQDN}" "Industry standard recommends upgrading to 2048-bit RSA"
        else
            warn "DKIM key published, but payload length is unusual (${KEY_LEN} chars)" "$DKIM_RECORD"
        fi
    else
        fail "DKIM record found but missing public key parameter (p=)" "$DKIM_RECORD"
    fi
else
    fail "DKIM public key missing at ${DKIM_FQDN}" "Run 'relay generate-dkim -domain ${DOMAIN} -selector ${SELECTOR}' and publish the resulting TXT record."
fi

echo ""

# ── 4. DMARC Policy Enforcement ───────────────────────────────────────────────
echo "${BLUE}[4/5] Auditing DMARC Policy...${NC}"
DMARC_FQDN="_dmarc.${DOMAIN}"
DMARC_JSON=$(doh_query "$DMARC_FQDN" "TXT")
DMARC_RECORD=$(echo "$DMARC_JSON" | jq -r '.Answer[]? | select(.type == 16) | .data' 2>/dev/null | tr -d '"' || true)

if [ -n "$DMARC_RECORD" ] && echo "$DMARC_RECORD" | grep -qi 'v=DMARC1'; then
    case "$DMARC_RECORD" in
        *"p=reject"*)
            pass "DMARC policy set to strict rejection (p=reject)" "$DMARC_RECORD" ;;
        *"p=quarantine"*)
            pass "DMARC policy set to quarantine (p=quarantine)" "$DMARC_RECORD" ;;
        *"p=none"*)
            warn "DMARC policy set to monitoring only (p=none)" "Upgrade to p=quarantine or p=reject once SPF/DKIM are verified in production." ;;
        *)
            warn "DMARC policy parameter unrecognized" "$DMARC_RECORD" ;;
    esac
else
    fail "DMARC policy missing at ${DMARC_FQDN}" "Add DNS TXT Record: ${DMARC_FQDN}. IN TXT \"v=DMARC1; p=quarantine; rua=mailto:admin@${DOMAIN}; pct=100\""
fi

echo ""

# ── 5. PTR / FCrDNS Check ─────────────────────────────────────────────────────
echo "${BLUE}[5/5] Auditing Reverse DNS (FCrDNS)...${NC}"
if [ -z "$VPS_IP" ]; then
    # Try resolving A record of MAIL_HOSTNAME
    HOST_JSON=$(doh_query "$MAIL_HOSTNAME" "A")
    VPS_IP=$(echo "$HOST_JSON" | jq -r '.Answer[]? | select(.type == 1) | .data' 2>/dev/null | head -n1 || true)
fi

if [ -n "$VPS_IP" ]; then
    # Build in-addr.arpa format
    O1=$(echo "$VPS_IP" | awk -F. '{print $1}')
    O2=$(echo "$VPS_IP" | awk -F. '{print $2}')
    O3=$(echo "$VPS_IP" | awk -F. '{print $3}')
    O4=$(echo "$VPS_IP" | awk -F. '{print $4}')
    ARPA="${O4}.${O3}.${O2}.${O1}.in-addr.arpa"

    PTR_JSON=$(doh_query "$ARPA" "PTR")
    PTR_RECORD=$(echo "$PTR_JSON" | jq -r '.Answer[]? | select(.type == 12) | .data' 2>/dev/null | sed 's/\.$//' | head -n1 || true)

    if [ -n "$PTR_RECORD" ]; then
        if [ "$PTR_RECORD" = "$MAIL_HOSTNAME" ]; then
            pass "Forward-Confirmed Reverse DNS (FCrDNS) aligned" "${VPS_IP} <-> ${MAIL_HOSTNAME}"
        else
            warn "PTR record resolves but does not match ${MAIL_HOSTNAME}" "Current PTR: ${PTR_RECORD} (Expected: ${MAIL_HOSTNAME}). Update rDNS with your VPS provider."
        fi
    else
        warn "No PTR record found for ${VPS_IP}" "Configure Reverse DNS (PTR) for ${VPS_IP} -> ${MAIL_HOSTNAME} in your hosting provider console."
    fi
else
    warn "Skipped PTR check: unable to detect VPS IP" "Specify via --ip <public_ip> or ensure ${MAIL_HOSTNAME} has a public A record."
fi

echo ""
echo "${CYAN}======================================================================${NC}"
echo "${BOLD}Diagnostic Summary:${NC} ${TOTAL_CHECKS} Checks, ${GREEN}${PASSED_CHECKS} Passed${NC}, ${RED}${FAILED_CHECKS} Failed${NC}, ${YELLOW}${WARNINGS} Warnings${NC}"
echo "${CYAN}======================================================================${NC}"

if [ "$FAILED_CHECKS" -gt 0 ]; then
    exit 1
fi
exit 0
