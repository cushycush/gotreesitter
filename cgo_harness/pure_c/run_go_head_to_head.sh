#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

FUNC_COUNT="${1:-500}"
FULL_ITERS="${2:-2000}"
INC_ITERS="${3:-20000}"

echo "== gotreesitter (pure Go) =="
(
  cd "$REPO_ROOT"
  go test . -run '^$' \
    -bench '^(BenchmarkGoParseFull|BenchmarkGoParseIncrementalSingleByteEdit|BenchmarkGoParseIncrementalRandomSingleByteEdit|BenchmarkGoParseIncrementalNoEdit)$' \
    -benchmem -count=1
)

echo
echo "== tree-sitter C runtime (pure C, no cgo) =="
"$SCRIPT_DIR/run_go_benchmark.sh" "$FUNC_COUNT" "$FULL_ITERS" "$INC_ITERS"
