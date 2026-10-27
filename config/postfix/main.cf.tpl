# ==============================================================================
# DROP-RELAY — POSTFIX MAIN CONFIGURATION TEMPLATE
# ==============================================================================
compatibility_level = 3.9

# ── Identity & Domain Context ─────────────────────────────────────────────────
myhostname = ${RELAY_HOSTNAME}
mydomain = ${RELAY_DOMAIN}
myorigin = $mydomain

mydestination = localhost.$mydomain, localhost
local_transport = error:local system mail delivery is disabled

# ── Virtual Mailbox Domain Configuration ──────────────────────────────────────
virtual_mailbox_domains = $mydomain
virtual_mailbox_maps = static:OK
virtual_transport = relay-ingest:dummy
relay-ingest_destination_recipient_limit = 1
recipient_delimiter = +
message_size_limit = ${MAX_MESSAGE_SIZE}

# ── Network & Trust Configuration ─────────────────────────────────────────────
inet_interfaces = all
inet_protocols = ipv4
mynetworks = 127.0.0.0/8 [::1]/128

# ── Cyrus SASL Submission Authentication ──────────────────────────────────────
smtpd_sasl_auth_enable = yes
smtpd_sasl_type = cyrus
smtpd_sasl_path = smtpd
smtpd_sasl_security_options = noanonymous
broken_sasl_auth_clients = yes
smtpd_sasl_local_domain = $mydomain

# ── Anti-Relay & Recipient Restrictions ───────────────────────────────────────
smtpd_helo_required = yes
disable_vrfy_command = yes
smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination
smtpd_recipient_restrictions =
    reject_non_fqdn_recipient,
    reject_unknown_recipient_domain,
    permit_mynetworks,
    permit_sasl_authenticated,
    reject_unauth_destination

# ── Inbound (Port 25) & Submission (Port 587) TLS ─────────────────────────────
smtpd_tls_security_level = may
smtpd_tls_cert_file = ${TLS_CERT_PATH}
smtpd_tls_key_file = ${TLS_KEY_PATH}
smtpd_tls_protocols = !SSLv2, !SSLv3, !TLSv1, !TLSv1.1
smtpd_tls_ciphers = high
smtpd_tls_mandatory_protocols = !SSLv2, !SSLv3, !TLSv1, !TLSv1.1
smtpd_tls_mandatory_ciphers = high
smtpd_tls_session_cache_database = lmdb:$data_directory/smtpd_scache

# ── Outbound Remote Delivery TLS (Direct MX) ──────────────────────────────────
smtp_tls_security_level = may
smtp_tls_protocols = !SSLv2, !SSLv3, !TLSv1, !TLSv1.1
smtp_tls_ciphers = high
smtp_tls_mandatory_protocols = !SSLv2, !SSLv3, !TLSv1, !TLSv1.1
smtp_tls_mandatory_ciphers = high
smtp_tls_CAfile = /etc/ssl/certs/ca-certificates.crt
smtp_tls_session_cache_database = lmdb:$data_directory/smtp_scache
smtp_tls_note_starttls_offer = yes

# ── OpenDKIM Milter Integration ───────────────────────────────────────────────
milter_default_action = accept
milter_protocol = 6
smtpd_milters = inet:127.0.0.1:8891
non_smtpd_milters = $smtpd_milters

# ── Queue Lifecycle & Rate Limiting ───────────────────────────────────────────
maximal_queue_lifetime = 2d
bounce_queue_lifetime = 1d
delay_warning_time = 0
default_destination_concurrency_limit = 5
default_destination_rate_delay = 1s

anvil_rate_time_unit = 60s
smtpd_client_message_rate_limit = 100
smtpd_client_connection_rate_limit = 30

# ── Privacy & Header Hygiene ──────────────────────────────────────────────────
header_checks = pcre:/etc/postfix/header_checks.pcre

# ── Server Metadata ───────────────────────────────────────────────────────────
smtpd_banner = $myhostname ESMTP drop-relay
biff = no
append_dot_mydomain = no
readme_directory = no
