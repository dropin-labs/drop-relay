# ==============================================================================
# DROP-RELAY — OPENDKIM CONFIGURATION TEMPLATE
# ==============================================================================
Syslog                  yes
SyslogSuccess           yes
LogWhy                  yes
UMask                   002

# Sign outbound mail (s) AND verify inbound DKIM signatures on Port 25 (v)
Mode                    sv
Canonicalization        relaxed/relaxed
SignatureAlgorithm      rsa-sha256
OversignHeaders         From

# Socket Postfix milter connects to
Socket                  inet:8891@127.0.0.1

# Tables (auto-hydrated at startup)
KeyTable                /etc/opendkim/KeyTable
SigningTable            refile:/etc/opendkim/SigningTable
ExternalIgnoreList      /etc/opendkim/TrustedHosts
InternalHosts           /etc/opendkim/TrustedHosts

# Process identity
UserID                  opendkim:opendkim

# Do not send unsolicited verification reports
SendReports             no
SoftwareHeader          no
AutoRestart             yes
AutoRestartRate         10/1h
