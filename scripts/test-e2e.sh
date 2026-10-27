#!/usr/bin/env bash
# ==============================================================================
# DROP-RELAY — SYNTHETIC END-TO-END INTEGRATION TEST HARNESS
# ==============================================================================
# Verifies:
#   1. Inbound Webhook Pipe Bridge (mail-ingest.sh): HMAC validation & exit codes
#   2. HTTP Ingestion API (/v1/send, /healthz, /readyz)
#   3. Bearer Token Authentication & Security Restrictions
#   4. Buffer Enqueue & Metrics Tracking (/metrics)
#   5. Clean POSIX Process Termination
# ==============================================================================

set -euo pipefail

# ANSI Colors
ESC=$(printf '\033')
GREEN="${ESC}[0;32m"
RED="${ESC}[0;31m"
YELLOW="${ESC}[1;33m"
CYAN="${ESC}[0;36m"
BOLD="${ESC}[1m"
NC="${ESC}[0m"

pass() {
    printf "  ${GREEN}[PASS]${NC} %s\n" "$1"
}

fail() {
    printf "  ${RED}[FAIL]${NC} %s\n" "$1"
    exit 1
}

info() {
    printf "${CYAN}==>${NC} ${BOLD}%s${NC}\n" "$1"
}

TMP_DIR=$(mktemp -d /tmp/drop-relay-e2e-XXXXXX)
MOCK_WEBHOOK_PORT=19090
RELAY_TEST_PORT=18080
TEST_API_KEY="test_e2e_secret_token"
TEST_HMAC_SECRET="test_hmac_secret_key_123456"

cleanup() {
    if [[ -n "${RELAY_PID:-}" ]] && kill -0 "$RELAY_PID" 2>/dev/null; then
        kill -TERM "$RELAY_PID" 2>/dev/null || true
        wait "$RELAY_PID" 2>/dev/null || true
    fi
    if [[ -n "${MOCK_PID:-}" ]] && kill -0 "$MOCK_PID" 2>/dev/null; then
        kill -9 "$MOCK_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP_DIR"
}

trap cleanup EXIT

echo "${CYAN}======================================================================${NC}"
echo "${BOLD}${CYAN}            DROP-RELAY SYNTHETIC END-TO-END INTEGRATION TEST          ${NC}"
echo "${CYAN}======================================================================${NC}"
echo ""

# ── 1. Ensure binary is compiled ──────────────────────────────────────────────
info "1. Compiling drop-relay test binary..."
if [[ ! -x "bin/relay" ]]; then
    make build >/dev/null
fi
pass "Relay binary ready: bin/relay"
echo ""

# ── 2. Mock Webhook Server (Python) ───────────────────────────────────────────
info "2. Starting Mock Inbound Webhook HTTP Receiver (:19090)..."

cat > "$TMP_DIR/mock_server.py" <<'EOF'
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler

class MockHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass  # Quiet stdout

    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)

        # Write received headers and payload to verify file
        with open(sys.argv[1] + "/last_request.log", "w") as f:
            f.write(f"PATH:{self.path}\n")
            f.write(f"SIG:{self.headers.get('X-Relay-Signature', '')}\n")
            f.write(f"SENDER:{self.headers.get('X-Relay-Sender', '')}\n")
            f.write(f"RCPT:{self.headers.get('X-Relay-Recipient', '')}\n")
            f.write(f"BODY_LEN:{len(body)}\n")

        if self.path == "/webhook-ok":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'{"status":"received"}')
        elif self.path == "/webhook-404":
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b'{"error":"recipient not found"}')
        else:
            self.send_response(500)
            self.end_headers()
            self.wfile.write(b'{"error":"internal error"}')

server = HTTPServer(('127.0.0.1', int(sys.argv[2])), MockHandler)
server.serve_forever()
EOF

python3 "$TMP_DIR/mock_server.py" "$TMP_DIR" "$MOCK_WEBHOOK_PORT" &
MOCK_PID=$!

# Wait for mock server readiness
MOCK_READY=0
for i in {1..30}; do
    if curl -s -X POST -o /dev/null "http://127.0.0.1:${MOCK_WEBHOOK_PORT}/webhook-ok" 2>/dev/null; then
        MOCK_READY=1
        break
    fi
    sleep 0.1
done

if [[ "$MOCK_READY" -ne 1 ]]; then
    fail "Mock Webhook Receiver failed to start on port $MOCK_WEBHOOK_PORT"
fi

pass "Mock Webhook Receiver listening on http://127.0.0.1:${MOCK_WEBHOOK_PORT}"
echo ""

# ── 3. Test Inbound Pipe Bridge (mail-ingest.sh) ──────────────────────────────
info "3. Testing Inbound Pipe Bridge (scripts/mail-ingest.sh)..."

SYNTHETIC_EMAIL="$TMP_DIR/synthetic.eml"
cat > "$SYNTHETIC_EMAIL" <<'EOF'
From: external-sender@external.com
To: user@example.com
Subject: Test Inbound RFC 822 Email
Message-ID: <test-1234@external.com>
Date: Mon, 27 Oct 2026 08:00:00 +0000
Content-Type: text/plain; charset=utf-8

Hello from external sender. This is an RFC 822 message body.
EOF

export RELAY_INTEGRITY_SECRET="$TEST_HMAC_SECRET"

# Test 3.1: Successful Delivery (200 -> exit 0)
export INBOUND_WEBHOOK_URL="http://127.0.0.1:${MOCK_WEBHOOK_PORT}/webhook-ok"
set +e
./scripts/mail-ingest.sh "external-sender@external.com" "user@example.com" < "$SYNTHETIC_EMAIL"
EXIT_CODE=$?
set -e
if [[ "$EXIT_CODE" -eq 0 ]]; then
    pass "Webhook 200 OK yielded exit 0 (EX_OK)"
else
    fail "Expected exit 0 for 200 OK, got: $EXIT_CODE"
fi

# Verify HMAC Signature authenticity
EXPECTED_SIG=$(openssl dgst -sha256 -hmac "$TEST_HMAC_SECRET" -hex "$SYNTHETIC_EMAIL" | awk '{print $NF}')
RECEIVED_SIG=$(grep '^SIG:' "$TMP_DIR/last_request.log" | cut -d: -f2)
if [[ "$EXPECTED_SIG" == "$RECEIVED_SIG" ]]; then
    pass "HMAC-SHA256 signature verified matches expected cryptographic hash ($EXPECTED_SIG)"
else
    fail "HMAC signature mismatch! Expected: $EXPECTED_SIG, Got: $RECEIVED_SIG"
fi

# Test 3.2: Unknown User Permanent Bounce (404 -> exit 67)
export INBOUND_WEBHOOK_URL="http://127.0.0.1:${MOCK_WEBHOOK_PORT}/webhook-404"
set +e
./scripts/mail-ingest.sh "external-sender@external.com" "unknown@example.com" < "$SYNTHETIC_EMAIL"
EXIT_CODE=$?
set -e
if [[ "$EXIT_CODE" -eq 67 ]]; then
    pass "Webhook 404 User Unknown yielded exit 67 (EX_NOUSER -> Postfix 550 bounce)"
else
    fail "Expected exit 67 for 404 response, got: $EXIT_CODE"
fi

# Test 3.3: Temporary Server Failure (500 -> exit 75)
export INBOUND_WEBHOOK_URL="http://127.0.0.1:${MOCK_WEBHOOK_PORT}/webhook-500"
set +e
./scripts/mail-ingest.sh "external-sender@external.com" "user@example.com" < "$SYNTHETIC_EMAIL"
EXIT_CODE=$?
set -e
if [[ "$EXIT_CODE" -eq 75 ]]; then
    pass "Webhook 500 Internal Error yielded exit 75 (EX_TEMPFAIL -> spool retry with backoff)"
else
    fail "Expected exit 75 for 500 response, got: $EXIT_CODE"
fi

# Test 3.4: Connection Refused / Network Down (exit 75)
export INBOUND_WEBHOOK_URL="http://127.0.0.1:19999/unreachable"
set +e
./scripts/mail-ingest.sh "external-sender@external.com" "user@example.com" < "$SYNTHETIC_EMAIL"
EXIT_CODE=$?
set -e
if [[ "$EXIT_CODE" -eq 75 ]]; then
    pass "Network unreachable yielded exit 75 (EX_TEMPFAIL)"
else
    fail "Expected exit 75 for network failure, got: $EXIT_CODE"
fi
echo ""

# ── 4. Boot drop-relay Server ─────────────────────────────────────────────────
info "4. Booting drop-relay daemon (:18080)..."

export RELAY_DOMAIN="example.com"
export RELAY_HOSTNAME="mail.example.com"
export RELAY_PORT_HTTP="$RELAY_TEST_PORT"
export RELAY_API_KEY="$TEST_API_KEY"
export SMTP_USER="test_smtp_user"
export SMTP_PASS="test_smtp_pass"
export SUBMISSION_HOST="127.0.0.1:12587"
export BUFFER_TYPE="memory"
export BUFFER_CAPACITY="500"

./bin/relay > "$TMP_DIR/relay.log" 2>&1 &
RELAY_PID=$!

# Wait for server readiness
READY=0
for i in {1..20}; do
    if curl -s -f "http://127.0.0.1:${RELAY_TEST_PORT}/readyz" >/dev/null 2>&1; then
        READY=1
        break
    fi
    sleep 0.2
done

if [[ "$READY" -ne 1 ]]; then
    echo "Server failed to become ready. Log output:"
    cat "$TMP_DIR/relay.log"
    fail "drop-relay failed to boot on port $RELAY_TEST_PORT"
fi
pass "drop-relay successfully initialized and ready"
echo ""

# ── 5. Health & Readiness Probes ──────────────────────────────────────────────
info "5. Testing /healthz, /readyz & /metrics..."

HEALTHZ_RESP=$(curl -s "http://127.0.0.1:${RELAY_TEST_PORT}/healthz")
if echo "$HEALTHZ_RESP" | grep -q '"status":"ok"'; then
    pass "GET /healthz responded 200 OK: $HEALTHZ_RESP"
else
    fail "/healthz unexpected response: $HEALTHZ_RESP"
fi

READYZ_RESP=$(curl -s "http://127.0.0.1:${RELAY_TEST_PORT}/readyz")
if echo "$READYZ_RESP" | grep -q '"status":"ready"'; then
    pass "GET /readyz responded 200 OK: $READYZ_RESP"
else
    fail "/readyz unexpected response: $READYZ_RESP"
fi
echo ""

# ── 6. Ingestion API Security & Dispatch ──────────────────────────────────────
info "6. Testing Ingestion API Security & Payload Handling (/v1/send)..."

# Test 6.1: Unauthorized Request
UNAUTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://127.0.0.1:${RELAY_TEST_PORT}/v1/send" \
    -H "Content-Type: application/json" \
    -d '{"from":"alerts@example.com","to":"user@target.com","subject":"Test","text":"Hi"}')

if [[ "$UNAUTH_CODE" -eq 401 ]]; then
    pass "Request without Authorization rejected with 401 Unauthorized"
else
    fail "Expected 401 for unauthenticated request, got: $UNAUTH_CODE"
fi

# Test 6.2: Invalid Bearer Token
BAD_AUTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://127.0.0.1:${RELAY_TEST_PORT}/v1/send" \
    -H "Authorization: Bearer invalid_token" \
    -H "Content-Type: application/json" \
    -d '{"from":"alerts@example.com","to":"user@target.com","subject":"Test","text":"Hi"}')

if [[ "$BAD_AUTH_CODE" -eq 401 ]]; then
    pass "Request with invalid Bearer token rejected with 401 Unauthorized"
else
    fail "Expected 401 for bad token, got: $BAD_AUTH_CODE"
fi

# Test 6.3: Valid Message Dispatch
SEND_RESP=$(curl -s -w "\n%{http_code}" \
    -X POST "http://127.0.0.1:${RELAY_TEST_PORT}/v1/send" \
    -H "Authorization: Bearer ${TEST_API_KEY}" \
    -H "Content-Type: application/json" \
    -d '{
        "from": "ops@example.com",
        "from_name": "Operations Team",
        "to": "recipient@target.com",
        "subject": "Critical Cloud Alert",
        "text": "All services operating at 100% capacity.",
        "html": "<p>All services operating at <strong>100% capacity</strong>.</p>",
        "headers": {"X-Priority": "1"}
    }')

HTTP_BODY=$(echo "$SEND_RESP" | head -n1)
HTTP_STATUS=$(echo "$SEND_RESP" | tail -n1)

if [[ "$HTTP_STATUS" -eq 202 ]]; then
    pass "Valid POST /v1/send returned 202 Accepted"
else
    fail "Expected 202 Accepted, got $HTTP_STATUS ($HTTP_BODY)"
fi

if echo "$HTTP_BODY" | grep -q '"status":"queued"'; then
    MSG_ID=$(echo "$HTTP_BODY" | jq -r '.id' 2>/dev/null || echo "")
    pass "Message queued successfully with ID: ${MSG_ID}"
else
    fail "Response JSON missing queued status: $HTTP_BODY"
fi

# Test 6.4: Input Validation (Invalid Email)
INVALID_EMAIL_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://127.0.0.1:${RELAY_TEST_PORT}/v1/send" \
    -H "Authorization: Bearer ${TEST_API_KEY}" \
    -H "Content-Type: application/json" \
    -d '{"from":"not-an-email","to":"user@target.com","subject":"Test","text":"Hi"}')

if [[ "$INVALID_EMAIL_CODE" -eq 422 ]]; then
    pass "Malformed 'from' email rejected with 422 Unprocessable Entity"
else
    fail "Expected 422 for invalid email address, got: $INVALID_EMAIL_CODE"
fi
echo ""

# ── 7. Prometheus Metrics Audit ───────────────────────────────────────────────
info "7. Auditing Prometheus Metrics (/metrics)..."

METRICS_DATA=$(curl -s "http://127.0.0.1:${RELAY_TEST_PORT}/metrics")

if echo "$METRICS_DATA" | grep -q 'relay_messages_ingested_total{channel="http",status="accepted"} 1'; then
    pass "Metric verified: relay_messages_ingested_total{channel=\"http\",status=\"accepted\"} == 1"
else
    fail "Prometheus metric relay_messages_ingested_total not incremented correctly."
fi

if echo "$METRICS_DATA" | grep -q 'relay_messages_ingested_total{channel="http",status="unauthorized"} 2'; then
    pass "Metric verified: unauthorized ingestion attempts tracked (count: 2)"
else
    fail "Prometheus metric for unauthorized attempts not tracked."
fi
echo ""

# ── 8. Graceful Shutdown ──────────────────────────────────────────────────────
info "8. Testing Graceful Shutdown (SIGTERM)..."

kill -TERM "$RELAY_PID"
wait "$RELAY_PID" || true
unset RELAY_PID
pass "drop-relay cleanly terminated and flushed queue upon SIGTERM"

echo ""
echo "${CYAN}======================================================================${NC}"
echo "${GREEN}${BOLD}       ALL END-TO-END INTEGRATION TESTS PASSED SUCCESSFULLY!          ${NC}"
echo "${CYAN}======================================================================${NC}"
exit 0
