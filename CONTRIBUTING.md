# Contributing to drop-relay

Thank you for your interest in contributing to `drop-relay`! We welcome issues, bug fixes, feature proposals, and documentation enhancements.

`drop-relay` is engineered with a **Minimalist, High-Signal Standard** (zero-bloat, config-first, and deterministic error handling). Please read this guide before submitting a pull request.

---

## 1. Guiding Principles

1. **Anti-Bloat Policy**: Avoid introducing external dependencies when the Go standard library (`net/http`, `net/smtp`, `crypto/*`, `sync`) is sufficient.
2. **Deterministic Security**: Zero tolerance for unauthenticated endpoints, plain-text credential leaks, or missing capability drops.
3. **Reproducibility**: All changes must pass unit tests, race detector, synthetic E2E tests, and Trivy security audits cleanly.

---

## 2. Development Setup

### Prerequisites
* **Go**: `1.24+`
* **Docker & Docker Compose**: `v20+` / `v2.20+`
* **Make** and standard POSIX shell tools (`curl`, `jq`, `openssl`)

### Local Workflow
```bash
# Clone the repository
git clone https://github.com/dropin-labs/drop-relay.git
cd drop-relay

# Install / verify Go dependencies
go mod download
go mod verify

# Build static binary locally
make build

# Run unit tests
make test

# Run unit tests with race detection
make test-race

# Run synthetic end-to-end integration test (requires python3, bash, curl)
make test-e2e

# Run Trivy vulnerability & secret scan
make scan-fs
```

---

## 3. Pull Request Guidelines

1. **Branch Naming**: Use descriptive prefixes:
   * `feat/short-description` (new features)
   * `fix/short-description` (bug fixes)
   * `sec/short-description` (security enhancements)
   * `perf/short-description` (performance optimizations)
   * `docs/short-description` (documentation improvements)
2. **Conventional Commits**: Format commit messages according to the [Conventional Commits](https://www.conventionalcommits.org/) specification:
   ```
   feat(buffer): add persistent lmdb spool backend
   fix(ingest): normalize crlf line breaks in text body
   sec(crypto): enforce 2048-bit minimum for rsa dkim keys
   docs(readme): add dns-over-https diagnostic examples
   ```
3. **Atomic Changes**: Keep PRs focused on a single change or fix. Split large refactors into smaller, reviewable increments.
4. **Testing Required**:
   * Add unit tests in `internal/<package>/<package>_test.go` for all new logic.
   * If modifying the ingestion API or delivery worker, update `scripts/test-e2e.sh`.

---

## 4. Pre-Submission Checklist

Before submitting your PR, ensure all checks pass locally:

```bash
make lint       # Runs 'go vet ./...'
make test       # Runs unit test suite
make test-race  # Validates concurrency safety
make test-e2e   # Validates full end-to-end lifecycle
make scan-fs    # Audits dependencies and secrets via Trivy
```

All PRs run automated GitHub Actions workflows and will only be merged when all checks are green.

---

## 5. Security Vulnerabilities

If you discover a security vulnerability, please **do not** open a public issue. Follow our responsible disclosure protocol in [SECURITY.md](SECURITY.md) or open a [Private GitHub Advisory](https://github.com/dropin-labs/drop-relay/security/advisories/new).
