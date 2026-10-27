# Security Policy & DevSecOps Architecture

`drop-relay` is engineered for critical, sovereign email delivery and webhook bridging. Security, cryptographic integrity, and supply chain assurance are foundational pillars.

---

## Supported Versions

Only the latest minor release line receives proactive security patches:

| Version | Supported          |
| :------ | :----------------- |
| `0.1.x` | :white_check_mark: |
| `< 0.1` | :x:                |

---

## Reporting Vulnerabilities

If you discover a security vulnerability in `drop-relay`, please report it responsibly:

* **GitHub Security Advisory**: Open a private advisory at [`https://github.com/dropin-labs/drop-relay/security/advisories/new`](https://github.com/dropin-labs/drop-relay/security/advisories/new).
* **Direct Email**: Send encrypted details to `security@dropin.rw`.

### Response SLA
* **Initial Assessment**: Within 24 hours.
* **Fix & Advisory Disclosure**: Within 72 hours for Critical/High severity CVEs.

Please do not disclose vulnerabilities publicly until a patch and advisory have been published.

---

## DevSecOps & Automated Vulnerability Scanning

`drop-relay` implements continuous security auditing via **Aqua Security Trivy**:

### 1. Multi-Vector Scanning Scope
* **Container Image (`scan-type: image`)**: Scans runtime OS packages (Alpine Linux) and compiled Go static binaries for known CVEs.
* **Repository Filesystem (`scan-type: fs`)**: Scans Go module dependencies (`go.mod`, `go.sum`) for supply-chain vulnerabilities.
* **Secret Leak Detection (`scanners: secret`)**: Scans commits, configs, and fixtures for credentials, private keys, or tokens.
* **Misconfiguration Audit (`scanners: misconfig`)**: Validates Dockerfiles, Compose specs, and GitHub Actions workflows against CIS benchmarks.

### 2. Zero-Tolerance Severity Gate
CI/CD workflows strictly enforce exit code `1` on **HIGH** or **CRITICAL** findings:

```yaml
# trivy.yaml
exit-code: 1
severity:
  - HIGH
  - CRITICAL
ignore-unfixed: true
```

* **SARIF Integration**: All scans output SARIF (`security-events: write`) uploaded directly to GitHub Advanced Security tab.
* **Release Gate**: Multi-arch publishing to GitHub Container Registry (`ghcr.io`) is gated behind a clean Trivy scan.

### 3. Local Security Auditing
Developers and platform engineers can reproduce CI scans locally without installing Trivy:

```bash
# Run full scan (filesystem + container image)
make scan

# Run repository filesystem and Go dependency scan only
make scan-fs

# Run container image CVE scan only
make scan-image

# Run direct CLI script with custom thresholds
./scripts/security-scan.sh --severity CRITICAL --format table
```

---

## Defense-in-Depth Architecture

1. **Minimal Surface Footprint**:
   Multi-stage build packaging a static Go binary into `alpine:3.21` yielding a <35MB compressed runtime with 0 known CVEs.
2. **Capability Drops & Rootless Safety**:
   Containers drop all capabilities (`cap_drop: [ALL]`) and retain only the minimal set required for MTA socket binding and Postfix setuid transitions:
   `NET_BIND_SERVICE`, `SETUID`, `SETGID`, `DAC_OVERRIDE`.
3. **Internal Topology Privacy**:
   Postfix PCRE filters (`config/postfix/header_checks.pcre`) strip RFC 1918 subnets, client IPs, and `User-Agent` headers.
4. **Cryptographic Validation**:
   * Outbound emails signed with 2048-bit RSA DKIM keys (`relaxed/relaxed` canonicalization).
   * Inbound pipe bridge computes HMAC-SHA256 (`X-Relay-Signature`) across raw `.eml` payloads to prevent spoofing.
