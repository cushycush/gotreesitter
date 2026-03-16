#!/usr/bin/env bash
#
# Run grammargen-vs-C parity tests inside a Docker container with bounded
# memory and CPU to prevent OOM blowups from large grammar generation or GLR
# stack explosions.
#
# Usage:
#   cgo_harness/docker/run_grammargen_c_parity.sh [OPTIONS]
#
# Options:
#   --memory MEM       Container memory limit (default: 8g)
#   --cpus N           CPU limit (default: 4)
#   --max-cases N      Max corpus samples per grammar (default: 20)
#   --max-bytes N      Max sample size in bytes (default: 262144)
#   --langs LANGS      Comma-separated language filter (default: all)
#   --ratchet-update   Write ratchet floor file after run
#   --label LABEL      Label for output directory
#   --timeout MINS     Test timeout in minutes (default: 45)
#   --src-dir DIR      Source directory (default: repo root)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Defaults.
MEMORY="8g"
CPUS="4"
MAX_CASES="20"
MAX_BYTES="262144"
LANGS=""
RATCHET_UPDATE=""
LABEL=""
TIMEOUT_MINS="45"
SRC_DIR="$REPO_ROOT"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --memory)      MEMORY="$2"; shift 2 ;;
        --cpus)        CPUS="$2"; shift 2 ;;
        --max-cases)   MAX_CASES="$2"; shift 2 ;;
        --max-bytes)   MAX_BYTES="$2"; shift 2 ;;
        --langs)       LANGS="$2"; shift 2 ;;
        --ratchet-update) RATCHET_UPDATE="1"; shift ;;
        --label)       LABEL="$2"; shift 2 ;;
        --timeout)     TIMEOUT_MINS="$2"; shift 2 ;;
        --src-dir)     SRC_DIR="$2"; shift 2 ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

IMAGE_TAG="gotreesitter-grammargen-cparity:latest"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
OUT_DIR="$REPO_ROOT/harness_out/grammargen_cparity/${TIMESTAMP}"
if [[ -n "$LABEL" ]]; then
    OUT_DIR="${OUT_DIR}-${LABEL}"
fi
mkdir -p "$OUT_DIR"

echo "=== grammargen C parity test ==="
echo "  memory:     $MEMORY"
echo "  cpus:       $CPUS"
echo "  max_cases:  $MAX_CASES"
echo "  max_bytes:  $MAX_BYTES"
echo "  langs:      ${LANGS:-all}"
echo "  ratchet:    ${RATCHET_UPDATE:-no}"
echo "  timeout:    ${TIMEOUT_MINS}m"
echo "  output:     $OUT_DIR"
echo ""

# Build docker image.
echo "--- Building Docker image ---"
docker build -t "$IMAGE_TAG" -f "$SCRIPT_DIR/Dockerfile" "$SCRIPT_DIR" 2>&1 | tail -5

# Clone grammar repos if needed.
GRAMMAR_PARITY_DIR="/tmp/grammar_parity"
if [[ ! -d "$GRAMMAR_PARITY_DIR" ]]; then
    echo "--- Grammar repos not found at $GRAMMAR_PARITY_DIR ---"
    echo "    Run: go test ./grammargen/ -run TestMultiGrammarImportPipeline -v"
    echo "    (which clones repos to /tmp/grammar_parity/)"
    echo ""
    echo "    Or set --src-dir to point to an existing checkout."
    exit 1
fi

# Build env vars for the container.
# When ratchet update is requested, redirect floors to /out so it persists
# (the source tree is mounted read-only).
FLOORS_PATH="/src/cgo_harness/testdata/grammargen_cgo_parity_floors.json"
if [[ -n "$RATCHET_UPDATE" ]]; then
    FLOORS_PATH="/out/grammargen_cgo_parity_floors.json"
    # Seed from existing floors if available.
    if [[ -f "$SRC_DIR/cgo_harness/testdata/grammargen_cgo_parity_floors.json" ]]; then
        cp "$SRC_DIR/cgo_harness/testdata/grammargen_cgo_parity_floors.json" "$OUT_DIR/grammargen_cgo_parity_floors.json"
    fi
fi

ENV_ARGS=(
    -e "GTS_GRAMMARGEN_CGO_ENABLE=1"
    -e "GTS_GRAMMARGEN_CGO_ROOT=/tmp/grammar_parity"
    -e "GTS_GRAMMARGEN_CGO_MAX_CASES=$MAX_CASES"
    -e "GTS_GRAMMARGEN_CGO_MAX_BYTES=$MAX_BYTES"
    -e "GTS_GRAMMARGEN_CGO_FLOORS_PATH=$FLOORS_PATH"
)
if [[ -n "$LANGS" ]]; then
    ENV_ARGS+=(-e "GTS_GRAMMARGEN_CGO_LANGS=$LANGS")
fi
if [[ -n "$RATCHET_UPDATE" ]]; then
    ENV_ARGS+=(-e "GTS_GRAMMARGEN_CGO_RATCHET_UPDATE=1")
fi

echo "--- Running tests in container ---"
set +e
docker run \
    --rm \
    --memory="$MEMORY" \
    --cpus="$CPUS" \
    --memory-swap="$MEMORY" \
    --oom-kill-disable=false \
    -v "$SRC_DIR:/src:ro" \
    -v "$GRAMMAR_PARITY_DIR:/tmp/grammar_parity:ro" \
    -v "$GRAMMAR_PARITY_DIR:/grammar_parity:ro" \
    -v "$OUT_DIR:/out" \
    "${ENV_ARGS[@]}" \
    "$IMAGE_TAG" \
    bash -c '
        set -euo pipefail
        cd /src/cgo_harness

        echo "=== container start: $(date -Iseconds) ===" | tee /out/container.log

        # Run the grammargen C parity test.
        go test . \
            -tags treesitter_c_parity \
            -run "^TestGrammargenCGOParity$" \
            -count=1 \
            -v \
            -timeout '"${TIMEOUT_MINS}"'m \
            2>&1 | tee -a /out/container.log

        EXIT_CODE=${PIPESTATUS[0]}

        echo "" >> /out/container.log
        echo "=== container end: $(date -Iseconds) exit=$EXIT_CODE ===" >> /out/container.log

        # Floors file is already at /out when ratchet update is enabled.
        # Copy the source-tree floors for reference when not ratcheting.
        if [[ -f /src/cgo_harness/testdata/grammargen_cgo_parity_floors.json ]]; then
            cp /src/cgo_harness/testdata/grammargen_cgo_parity_floors.json /out/floors_baseline.json 2>/dev/null || true
        fi

        exit $EXIT_CODE
    '
CONTAINER_EXIT=$?
set -e

# Save metadata.
cat > "$OUT_DIR/metadata.txt" <<EOF
timestamp: $TIMESTAMP
memory: $MEMORY
cpus: $CPUS
max_cases: $MAX_CASES
max_bytes: $MAX_BYTES
langs: ${LANGS:-all}
ratchet_update: ${RATCHET_UPDATE:-no}
timeout_mins: $TIMEOUT_MINS
exit_code: $CONTAINER_EXIT
EOF

echo ""
echo "=== Done (exit=$CONTAINER_EXIT) ==="
echo "  Output: $OUT_DIR"
echo "  Log:    $OUT_DIR/container.log"

exit "$CONTAINER_EXIT"
