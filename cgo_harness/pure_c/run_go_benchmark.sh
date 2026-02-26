#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

FUNC_COUNT="${1:-500}"
FULL_ITERS="${2:-2000}"
INC_ITERS="${3:-20000}"

MODDIR="$(cd "$ROOT_DIR" && go list -m -f '{{.Dir}}' github.com/smacker/go-tree-sitter)"
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

CFLAGS=(-O3 -DNDEBUG -I"$MODDIR" -w)
if [[ -n "${CFLAGS_EXTRA:-}" ]]; then
  # shellcheck disable=SC2206
  extra=(${CFLAGS_EXTRA})
  CFLAGS+=("${extra[@]}")
fi
CORE_SRCS=(
  alloc
  get_changed_ranges
  language
  lexer
  node
  parser
  stack
  subtree
  tree
  tree_cursor
  query
  wasm_store
)

for src in "${CORE_SRCS[@]}"; do
  gcc "${CFLAGS[@]}" -c "$MODDIR/$src.c" -o "$BUILD_DIR/$src.o"
done

gcc "${CFLAGS[@]}" -c "$MODDIR/golang/parser.c" -o "$BUILD_DIR/go_parser.o"
gcc "${CFLAGS[@]}" -c "$SCRIPT_DIR/go_benchmark.c" -o "$BUILD_DIR/bench.o"

gcc -O3 "$BUILD_DIR"/*.o -o "$BUILD_DIR/bench"
"$BUILD_DIR/bench" "$FUNC_COUNT" "$FULL_ITERS" "$INC_ITERS"
