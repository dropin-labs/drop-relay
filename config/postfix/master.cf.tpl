# ==============================================================================
# DROP-RELAY — POSTFIX MASTER CONFIGURATION TEMPLATE
# ==============================================================================
# service type  private unpriv  chroot  wakeup  maxproc command + args
#               (yes)   (yes)   (no)    (never) (100)
# ==============================================================================

# Port 25: Public Inbound MX Listener (Strictly for $mydomain virtual mailboxes)
smtp      inet  n       -       n       -       -       smtpd
  -o syslog_name=postfix/smtpd-inbound
  -o smtpd_tls_security_level=may
  -o smtpd_sasl_auth_enable=no
  -o smtpd_client_restrictions=permit
  -o smtpd_relay_restrictions=reject_non_fqdn_recipient,reject_unknown_recipient_domain,permit_mynetworks,reject_unauth_destination
  -o smtpd_recipient_restrictions=reject_non_fqdn_recipient,reject_unknown_recipient_domain,permit_mynetworks,reject_unauth_destination

# Port 587: Authenticated Submission Listener
submission inet n       -       n       -       -       smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_sasl_type=cyrus
  -o smtpd_sasl_security_options=noanonymous
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
  -o smtpd_relay_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject_unauth_destination
  -o milter_macro_daemon_name=ORIGINATING

# Inbound Webhook Pipe Transport
relay-ingest unix -     n       n       -       10      pipe
  flags=Rq user=nobody argv=/usr/local/bin/mail-ingest.sh ${sender} ${recipient}

# Core queue & delivery subsystems
pickup    unix  n       -       n       60      1       pickup
cleanup   unix  n       -       n       -       0       cleanup
qmgr      unix  n       -       n       300     1       qmgr
tlsmgr    unix  -       -       n       1000?   1       tlsmgr
rewrite   unix  -       -       n       -       -       trivial-rewrite
bounce    unix  -       -       n       -       0       bounce
defer     unix  -       -       n       -       0       bounce
trace     unix  -       -       n       -       0       bounce
verify    unix  -       -       n       -       1       verify
flush     unix  n       -       n       1000?   0       flush
proxymap  unix  -       -       n       -       -       proxymap
proxywrite unix -       -       n       -       1       proxymap
smtp      unix  -       -       n       -       -       smtp
relay     unix  -       -       n       -       -       smtp
showq     unix  n       -       n       -       -       showq
error     unix  -       -       n       -       -       error
retry     unix  -       -       n       -       -       error
discard   unix  -       -       n       -       -       discard
local     unix  -       n       n       -       -       local
virtual   unix  -       n       n       -       -       virtual
lmtp      unix  -       -       n       -       -       lmtp
anvil     unix  -       -       n       -       1       anvil
scache    unix  -       -       n       -       1       scache
postlog   pass  -       -       n       -       -       postlogd
