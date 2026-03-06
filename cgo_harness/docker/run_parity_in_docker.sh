#!/usr/bin/env bash
#
# Run cgo_harness parity tests inside a Docker container with bounded memory
# to prevent OOM from GLR stack explosions or large parse trees.
#
# Usage:
#   cgo_harness/docker/run_parity_in_docker.sh [OPTIONS]
#
# Options:
#   --memory MEM       Container memory limit (default: 8g)
#   --cpus N           CPU limit (default: 4)
#   --run PATTERN      Test pattern (default: TestParity)
#   --strict-scala     Enable strict Scala real-world parity
#   --label LABEL      Label for output directory
#   --timeout MINS     Test timeout in minutes (default: 30)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

MEMORY="8g"
CPUS="4"
RUN_PATTERN="TestParity"
STRICT_SCALA=""
LABEL=""
TIMEOUT_MINS="30"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --memory)        MEMORY="$2"; shift 2 ;;
        --cpus)          CPUS="$2"; shift 2 ;;
        --run)           RUN_PATTERN="$2"; shift 2 ;;
        --strict-scala)  STRICT_SCALA="1"; shift ;;
        --label)         LABEL="$2"; shift 2 ;;
        --timeout)       TIMEOUT_MINS="$2"; shift 2 ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

IMAGE_TAG="gotreesitter-parity:latest"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
OUT_DIR="$REPO_ROOT/harness_out/docker/${TIMESTAMP}"
if [[ -n "$LABEL" ]]; then
    OUT_DIR="${OUT_DIR}-${LABEL}"
fi
mkdir -p "$OUT_DIR"

echo "=== parity test (docker) ==="
echo "  memory:  $MEMORY"
echo "  cpus:    $CPUS"
echo "  run:     $RUN_PATTERN"
echo "  timeout: ${TIMEOUT_MINS}m"
echo "  output:  $OUT_DIR"
echo ""

docker build -t "$IMAGE_TAG" -f "$SCRIPT_DIR/Dockerfile" "$SCRIPT_DIR" 2>&1 | tail -5

ENV_ARGS=()
if [[ -n "$STRICT_SCALA" ]]; then
    ENV_ARGS+=(-e "GTS_PARITY_SCALA_REALWORLD_STRICT=1")
fi

set +e
docker run \
    --rm \
    --memory="$MEMORY" \
    --cpus="$CPUS" \
    --memory-swap="$MEMORY" \
    --oom-kill-disable=false \
    -v "$REPO_ROOT:/src:ro" \
    -v "$OUT_DIR:/out" \
    "${ENV_ARGS[@]}" \
    "$IMAGE_TAG" \
    bash -c '
        set -euo pipefail
        cd /src/cgo_harness
        echo "=== container start: $(date -Iseconds) ===" | tee /out/container.log

        go test . \
            -tags treesitter_c_parity \
            -run "'"$RUN_PATTERN"'" \
            -count=1 \
            -v \
            -timeout '"${TIMEOUT_MINS}"'m \
            2>&1 | tee -a /out/container.log

        EXIT_CODE=${PIPESTATUS[0]}
        echo "=== container end: $(date -Iseconds) exit=$EXIT_CODE ===" >> /out/container.log
        exit $EXIT_CODE
    '
CONTAINER_EXIT=$?
set -e

cat > "$OUT_DIR/metadata.txt" <<EOF
timestamp: $TIMESTAMP
memory: $MEMORY
cpus: $CPUS
run_pattern: $RUN_PATTERN
strict_scala: ${STRICT_SCALA:-no}
timeout_mins: $TIMEOUT_MINS
exit_code: $CONTAINER_EXIT
EOF

echo ""
echo "=== Done (exit=$CONTAINER_EXIT) ==="
echo "  Output: $OUT_DIR"
exit "$CONTAINER_EXIT"
