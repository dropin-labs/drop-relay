#!/usr/bin/env bash
# ==============================================================================
# DROP-RELAY — TRIVY DEVSECOPS SECURITY SCANNER
# ==============================================================================
# Audits repository filesystem, Go dependencies, configuration hygiene,
# secret leaks, and container image for CVEs using Trivy.
#
# Usage:
#   ./scripts/security-scan.sh [TARGET] [OPTIONS]
#
# Targets:
#   all               Run both filesystem and container image scans (default)
#   fs, repo          Scan repository filesystem, Go modules, configs, secrets
#   image             Scan the compiled container image
#
# Options:
#   --image <name>    Container image to scan (default: dropinlabs/drop-relay:latest)
#   --severity <list> Comma-separated severity thresholds (default: HIGH,CRITICAL)
#   --format <fmt>    Output format: table, json, sarif (default: table)
#   --exit-code <code> Exit code when vulnerabilities found (default: 1)
#   -h, --help        Show this help message
# ==============================================================================

set -euo pipefail

# ANSI color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

TARGET="all"
IMAGE_NAME="dropinlabs/drop-relay:latest"
SEVERITY="HIGH,CRITICAL"
FORMAT="table"
EXIT_CODE="1"
TRIVY_CONFIG="trivy.yaml"
CACHE_DIR="${HOME}/.cache/trivy"

show_help() {
    sed -n '2,19p' "$0" | sed 's/^# \?//'
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        all|fs|repo|image)
            TARGET="$1"
            shift
            ;;
        --image)
            IMAGE_NAME="$2"
            shift 2
            ;;
        --severity)
            SEVERITY="$2"
            shift 2
            ;;
        --format)
            FORMAT="$2"
            shift 2
            ;;
        --exit-code)
            EXIT_CODE="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            ;;
        *)
            echo -e "${RED}[ERROR] Unknown option: $1${NC}"
            show_help
            ;;
    esac
done

# Ensure cache directory exists
mkdir -p "$CACHE_DIR"

# Resolve Trivy runner (local binary or docker fallback)
run_trivy() {
    if command -v trivy >/dev/null 2>&1; then
        trivy "$@"
    elif command -v docker >/dev/null 2>&1; then
        docker run --rm \
            -v /var/run/docker.sock:/var/run/docker.sock \
            -v "$CACHE_DIR:/root/.cache/trivy" \
            -v "$(pwd):/workspace" \
            -w /workspace \
            aquasec/trivy:latest "$@"
    else
        echo -e "${RED}[FATAL] Neither 'trivy' CLI nor 'docker' is available on PATH.${NC}"
        exit 1
    fi
}

echo -e "${CYAN}======================================================================${NC}"
echo -e "${BOLD}${CYAN}            DROP-RELAY DEVSECOPS TRIVY SECURITY SCANNER               ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo -e " Target Mode:     ${BOLD}${TARGET}${NC}"
echo -e " Severity Filter: ${BOLD}${SEVERITY}${NC}"
echo -e " Output Format:   ${BOLD}${FORMAT}${NC}"
echo -e " Target Image:    ${BOLD}${IMAGE_NAME}${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""

SCAN_STATUS=0

# ── 1. Filesystem & Dependency Scan ───────────────────────────────────────────
if [[ "$TARGET" == "all" || "$TARGET" == "fs" || "$TARGET" == "repo" ]]; then
    echo -e "${BLUE}==>${NC} ${BOLD}1. Scanning Repository Filesystem, Go Dependencies & Secrets...${NC}"
    
    set +e
    run_trivy fs \
        --config "$TRIVY_CONFIG" \
        --severity "$SEVERITY" \
        --format "$FORMAT" \
        --exit-code "$EXIT_CODE" \
        .
    FS_EXIT=$?
    set -e

    if [[ "$FS_EXIT" -eq 0 ]]; then
        echo -e "  ${GREEN}[PASS] Repository filesystem, dependencies, and secrets are clean.${NC}\n"
    else
        echo -e "  ${RED}[FAIL] Vulnerabilities or misconfigurations detected in filesystem!${NC}\n"
        SCAN_STATUS=1
    fi
fi

# ── 2. Container Image Scan ───────────────────────────────────────────────────
if [[ "$TARGET" == "all" || "$TARGET" == "image" ]]; then
    echo -e "${BLUE}==>${NC} ${BOLD}2. Scanning Container Image (${IMAGE_NAME})...${NC}"
    
    # Check if target image exists locally
    if command -v docker >/dev/null 2>&1; then
        if ! docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
            echo -e "  ${YELLOW}[WARN] Image '${IMAGE_NAME}' not found locally. Building image first...${NC}"
            docker build -t "$IMAGE_NAME" -f docker/Dockerfile .
        fi
    fi

    set +e
    run_trivy image \
        --config "$TRIVY_CONFIG" \
        --severity "$SEVERITY" \
        --format "$FORMAT" \
        --exit-code "$EXIT_CODE" \
        "$IMAGE_NAME"
    IMG_EXIT=$?
    set -e

    if [[ "$IMG_EXIT" -eq 0 ]]; then
        echo -e "  ${GREEN}[PASS] Container image '${IMAGE_NAME}' is clean (0 CVEs).${NC}\n"
    else
        echo -e "  ${RED}[FAIL] High or Critical vulnerabilities detected in container image!${NC}\n"
        SCAN_STATUS=1
    fi
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo -e "${CYAN}======================================================================${NC}"
if [[ "$SCAN_STATUS" -eq 0 ]]; then
    echo -e "${GREEN}${BOLD}       ALL TRIVY DEVSECOPS SECURITY AUDITS PASSED CLEANLY!            ${NC}"
    echo -e "${CYAN}======================================================================${NC}"
    exit 0
else
    echo -e "${RED}${BOLD}       SECURITY AUDIT FAILED — CRITICAL/HIGH FINDINGS DETECTED!       ${NC}"
    echo -e "${CYAN}======================================================================${NC}"
    exit 1
fi
