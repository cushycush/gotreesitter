#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
REAL_RUNNER="$SCRIPT_DIR/run_single_grammar_parity.sh"
CGO_RUNNER="$SCRIPT_DIR/run_grammargen_c_parity.sh"

MODE="all"
LANGS_CSV="javascript,typescript,tsx,c,cpp,c_sharp,cobol,fortran"
MEMORY_LIMIT="8g"
REAL_TIMEOUT="15m"
REAL_MAX_CASES="25"
REAL_PROFILE="aggressive"
CGO_TIMEOUT_MINS="45"
CGO_MAX_CASES="20"
CGO_MAX_BYTES="262144"
REPORT_ROOT="$REPO_ROOT/cgo_harness/reports/focus_targets"
SEED_DIR=""
OFFLINE=0
BUILD_IMAGE=1
LR_SPLIT=0

usage() {
  cat <<'EOF'
Usage: run_grammargen_focus_targets.sh [options]

Run the high-value grammargen targets only, with safe isolation by default:
  javascript, typescript, tsx, c, cpp, c_sharp, cobol, fortran

Modes:
  all          Run real-corpus parity and direct grammargen-vs-C parity
  real-corpus  Run only per-grammar real-corpus parity
  cgo          Run only direct grammargen-vs-C parity

Options:
  --mode <m>             all|real-corpus|cgo (default: all)
  --langs <list>         Comma-separated subset of target languages
  --memory <limit>       Docker memory limit for both paths (default: 8g)
  --real-timeout <dur>   Real-corpus timeout per grammar (default: 15m)
  --real-max-cases <n>   Real-corpus max cases per grammar (default: 25)
  --profile <name>       smoke|balanced|aggressive (default: aggressive)
  --cgo-timeout <mins>   Direct C parity timeout minutes (default: 45)
  --cgo-max-cases <n>    Direct C parity max cases (default: 20)
  --cgo-max-bytes <n>    Direct C parity max sample bytes (default: 262144)
  --report-root <path>   Real-corpus report root (default: cgo_harness/reports/focus_targets)
  --seed-dir <path>      Seed grammar repos from a dir under repo root
  --offline              Do not clone missing grammar repos in containers
  --lr-split             Enable GTS_GRAMMARGEN_LR_SPLIT for real-corpus runs
  --no-build             Skip Docker image builds in the underlying runners
  --list                 Print canonical target languages and exit
  -h, --help             Show this help

Notes:
  - Real-corpus parity runs one grammar per container via run_single_grammar_parity.sh.
  - Direct C parity also runs one language per container via run_grammargen_c_parity.sh.
  - fortran is currently real-corpus-only; the direct grammargen-vs-C harness
    does not expose it yet.
EOF
}

canonical_lang() {
  local lang
  lang="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
  lang="${lang//[[:space:]]/}"
  case "$lang" in
    js) echo "javascript" ;;
    ts) echo "typescript" ;;
    c++|cplusplus) echo "cpp" ;;
    c#|csharp) echo "c_sharp" ;;
    c_lang) echo "c" ;;
    *) echo "$lang" ;;
  esac
}

is_supported_focus_lang() {
  case "$1" in
    javascript|typescript|tsx|c|cpp|c_sharp|cobol|fortran) return 0 ;;
    *) return 1 ;;
  esac
}

real_corpus_lang() {
  case "$1" in
    c) echo "c_lang" ;;
    *) echo "$1" ;;
  esac
}

supports_cgo_parity() {
  case "$1" in
    javascript|typescript|tsx|c|cpp|c_sharp|cobol) return 0 ;;
    *) return 1 ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) MODE="$2"; shift 2 ;;
    --langs) LANGS_CSV="$2"; shift 2 ;;
    --memory) MEMORY_LIMIT="$2"; shift 2 ;;
    --real-timeout) REAL_TIMEOUT="$2"; shift 2 ;;
    --real-max-cases) REAL_MAX_CASES="$2"; shift 2 ;;
    --profile) REAL_PROFILE="$2"; shift 2 ;;
    --cgo-timeout) CGO_TIMEOUT_MINS="$2"; shift 2 ;;
    --cgo-max-cases) CGO_MAX_CASES="$2"; shift 2 ;;
    --cgo-max-bytes) CGO_MAX_BYTES="$2"; shift 2 ;;
    --report-root) REPORT_ROOT="$2"; shift 2 ;;
    --seed-dir) SEED_DIR="$2"; shift 2 ;;
    --offline) OFFLINE=1; shift ;;
    --lr-split) LR_SPLIT=1; shift ;;
    --no-build) BUILD_IMAGE=0; shift ;;
    --list)
      printf '%s\n' javascript typescript tsx c cpp c_sharp cobol fortran
      exit 0
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$MODE" in
  all|real-corpus|cgo) ;;
  *)
    echo "invalid --mode: $MODE" >&2
    exit 2
    ;;
esac

declare -a TARGET_LANGS=()
declare -A seen_langs=()
IFS=',' read -r -a raw_langs <<< "$LANGS_CSV"
for raw_lang in "${raw_langs[@]}"; do
  lang="$(canonical_lang "$raw_lang")"
  if [[ -z "$lang" ]]; then
    continue
  fi
  if ! is_supported_focus_lang "$lang"; then
    echo "unsupported focus language: $raw_lang" >&2
    exit 2
  fi
  if [[ -n "${seen_langs[$lang]:-}" ]]; then
    continue
  fi
  seen_langs[$lang]=1
  TARGET_LANGS+=("$lang")
done

if [[ ${#TARGET_LANGS[@]} -eq 0 ]]; then
  echo "no target languages selected" >&2
  exit 2
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
REAL_REPORT_DIR="$REPORT_ROOT/$STAMP/real_corpus"

real_build_enabled="$BUILD_IMAGE"
cgo_build_enabled="$BUILD_IMAGE"

real_ok=0
real_fail=0
real_oom=0
real_skip=0

cgo_ok=0
cgo_fail=0
cgo_oom=0
cgo_skip=0

real_corpus_status_from_log() {
  local log_path="$1"
  local summary
  summary="$(grep -E 'real-corpus\[' "$log_path" 2>/dev/null | tail -1 || true)"
  if [[ -z "$summary" ]]; then
    echo fail
    return
  fi
  if [[ "$summary" =~ no-error[[:space:]]+([0-9]+)/([0-9]+),[[:space:]]+sexpr[[:space:]]+parity[[:space:]]+([0-9]+)/([0-9]+),[[:space:]]+deep[[:space:]]+parity[[:space:]]+([0-9]+)/([0-9]+) ]]; then
    local no_error="${BASH_REMATCH[1]}"
    local eligible_a="${BASH_REMATCH[2]}"
    local sexpr="${BASH_REMATCH[3]}"
    local eligible_b="${BASH_REMATCH[4]}"
    local deep="${BASH_REMATCH[5]}"
    local eligible_c="${BASH_REMATCH[6]}"
    if [[ "$no_error" == "$eligible_a" && "$sexpr" == "$eligible_b" && "$deep" == "$eligible_c" ]]; then
      echo ok
    else
      echo fail
    fi
    return
  fi
  echo fail
}

run_real_corpus_lang() {
  local lang="$1"
  local grammar log_path
  grammar="$(real_corpus_lang "$lang")"
  log_path="$REAL_REPORT_DIR/diag_${grammar}.log"

  mkdir -p "$REAL_REPORT_DIR"

  local -a args=(
    --memory "$MEMORY_LIMIT"
    --timeout "$REAL_TIMEOUT"
    --max-cases "$REAL_MAX_CASES"
    --profile "$REAL_PROFILE"
    --report-dir "$REAL_REPORT_DIR"
  )
  if [[ -n "$SEED_DIR" ]]; then
    args+=(--seed-dir "$SEED_DIR")
  fi
  if [[ "$OFFLINE" == "1" ]]; then
    args+=(--offline)
  fi
  if [[ "$LR_SPLIT" == "1" ]]; then
    args+=(--lr-split)
  fi
  if [[ "$real_build_enabled" == "0" ]]; then
    args+=(--no-build)
  fi
  args+=("$grammar")

  set +e
  "$REAL_RUNNER" "${args[@]}"
  local call_exit=$?
  set -e
  real_build_enabled=0

  local status="fail"
  if [[ -f "$log_path" ]]; then
    if grep -q '^oom_killed: true$' "$log_path"; then
      status="oom"
    elif grep -q '^exit_code: 0$' "$log_path"; then
      status="$(real_corpus_status_from_log "$log_path")"
    fi
  elif [[ "$call_exit" == "0" ]]; then
    status="fail"
  fi

  case "$status" in
    ok)
      ((real_ok+=1))
      echo "[real-corpus] $lang -> PARITY"
      ;;
    oom)
      ((real_oom+=1))
      echo "[real-corpus] $lang -> OOM"
      ;;
    *)
      ((real_fail+=1))
      echo "[real-corpus] $lang -> MISMATCH"
      ;;
  esac
}

run_cgo_lang() {
  local lang="$1"
  if ! supports_cgo_parity "$lang"; then
    ((cgo_skip+=1))
    echo "[cgo] $lang -> SKIP (not wired in direct grammargen-vs-C harness)"
    return 0
  fi

  local -a args=(
    --memory "$MEMORY_LIMIT"
    --max-cases "$CGO_MAX_CASES"
    --max-bytes "$CGO_MAX_BYTES"
    --langs "$lang"
    --timeout "$CGO_TIMEOUT_MINS"
    --label "focus-${lang}"
    --src-dir "$REPO_ROOT"
  )
  if [[ -n "$SEED_DIR" ]]; then
    args+=(--seed-dir "$SEED_DIR")
  fi
  if [[ "$OFFLINE" == "1" ]]; then
    args+=(--offline)
  fi
  if [[ "$cgo_build_enabled" == "0" ]]; then
    args+=(--no-build)
  fi

  set +e
  "$CGO_RUNNER" "${args[@]}"
  local exit_code=$?
  set -e
  cgo_build_enabled=0

  if [[ "$exit_code" == "0" ]]; then
    ((cgo_ok+=1))
    echo "[cgo] $lang -> OK"
  elif [[ "$exit_code" == "137" ]]; then
    ((cgo_oom+=1))
    echo "[cgo] $lang -> OOM"
  else
    ((cgo_fail+=1))
    echo "[cgo] $lang -> FAIL (exit=$exit_code)"
  fi
}

echo "Focused grammargen targets: ${TARGET_LANGS[*]}"
echo "mode=$MODE memory=$MEMORY_LIMIT offline=$OFFLINE lr_split=$LR_SPLIT"
echo ""

if [[ "$MODE" == "all" || "$MODE" == "real-corpus" ]]; then
  echo "=== Real-Corpus Parity (per-grammar isolation) ==="
  for lang in "${TARGET_LANGS[@]}"; do
    run_real_corpus_lang "$lang"
  done
  echo ""
fi

if [[ "$MODE" == "all" || "$MODE" == "cgo" ]]; then
  echo "=== Direct Grammargen-vs-C Parity (per-language isolation) ==="
  for lang in "${TARGET_LANGS[@]}"; do
    run_cgo_lang "$lang"
  done
  echo ""
fi

echo "=== Summary ==="
if [[ "$MODE" == "all" || "$MODE" == "real-corpus" ]]; then
  echo "real-corpus: ok=$real_ok fail=$real_fail oom=$real_oom skip=$real_skip report_dir=$REAL_REPORT_DIR"
fi
if [[ "$MODE" == "all" || "$MODE" == "cgo" ]]; then
  echo "cgo: ok=$cgo_ok fail=$cgo_fail oom=$cgo_oom skip=$cgo_skip"
fi

if [[ "$real_fail" -gt 0 || "$real_oom" -gt 0 || "$cgo_fail" -gt 0 || "$cgo_oom" -gt 0 ]]; then
  exit 1
fi
