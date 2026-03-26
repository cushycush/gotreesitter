#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<'EOF'
Usage: test_race_serial.sh [package-pattern ...] [-- go-test-args...]

Expand package patterns with `go list` and run `go test -race` one package at a
time with serial settings. This is the host-safe alternative to
`go test ./... -race`, which can fan out compile/test work and OOM developer
machines in this repo.

Examples:
  scripts/test_race_serial.sh
  scripts/test_race_serial.sh ./grammars -- -run '^TestTop50ParseSmokeNoErrors$' -v
  scripts/test_race_serial.sh ./grep ./cmd/benchgate
EOF
}

patterns=()
test_args=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      test_args=("$@")
      break
      ;;
    *)
      patterns+=("$1")
      shift
      ;;
  esac
done

if [[ ${#patterns[@]} -eq 0 ]]; then
  patterns=(./...)
fi

mapfile -t packages < <(
  cd "$REPO_ROOT"
  go list "${patterns[@]}"
)

if [[ ${#packages[@]} -eq 0 ]]; then
  echo "no packages matched: ${patterns[*]}" >&2
  exit 1
fi

export GOMAXPROCS="${GOMAXPROCS:-1}"

base_args=(-race -count=1 -p=1 -parallel=1)

echo "GOMAXPROCS=$GOMAXPROCS"
echo "packages=${#packages[@]}"

cd "$REPO_ROOT"
for pkg in "${packages[@]}"; do
  echo "==> go test ${base_args[*]} ${test_args[*]} $pkg"
  go test "${base_args[@]}" "${test_args[@]}" "$pkg"
done
