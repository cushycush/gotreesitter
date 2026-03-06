#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="$SCRIPT_DIR/run_parity_in_docker.sh"

IMAGE_TAG="gotreesitter/cgo-harness:go1.24-local"
MEMORY_LIMIT="8g"
CPUS_LIMIT="4"
PIDS_LIMIT="4096"
MAX_PARALLEL="2"
BUILD_IMAGE=1
STRICT_SCALA=0
RUN_REGEX=""
OUT_ROOT=""
declare -a EXPERIMENTS=()
declare -a CUSTOM_CMD=()

usage() {
  cat <<'EOF'
Usage: run_parity_experiments.sh [options]
       run_parity_experiments.sh [options] -- <custom command>

Run multiple parity experiments against different worktrees/repo roots with
bounded parallelism. Each experiment is defined as:

  --experiment <label>=<repo_root>

Examples:
  run_parity_experiments.sh \
    --experiment main=/home/me/work/gotreesitter \
    --experiment glr=/home/me/work/gts-glr \
    --max-parallel 2 --memory 6g

  run_parity_experiments.sh \
    --experiment scala=/home/me/work/gts-scala \
    --strict-scala \
    -- "cd /workspace/cgo_harness && go test . -tags treesitter_c_parity -run '^TestParityScalaRealWorldCorpus$' -count=1 -v"

Options:
  --experiment <label>=<repo_root>  Add an experiment (repeatable)
  --max-parallel <n>                Max concurrent experiments (default: 2)
  --image <tag>                     Docker image tag
  --memory <limit>                  Per-container memory limit (default: 8g)
  --cpus <count>                    Per-container CPU limit (default: 4)
  --pids <count>                    Per-container PID limit (default: 4096)
  --run <regex>                     go test -run regex (default command only)
  --strict-scala                    Include strict scala probe (default command only)
  --out-root <path>                 Shared artifact root for experiment runs
  --no-build                        Skip docker build step
  -h, --help                        Show help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --experiment)
      EXPERIMENTS+=("$2")
      shift 2
      ;;
    --max-parallel)
      MAX_PARALLEL="$2"
      shift 2
      ;;
    --image)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --memory)
      MEMORY_LIMIT="$2"
      shift 2
      ;;
    --cpus)
      CPUS_LIMIT="$2"
      shift 2
      ;;
    --pids)
      PIDS_LIMIT="$2"
      shift 2
      ;;
    --run)
      RUN_REGEX="$2"
      shift 2
      ;;
    --strict-scala)
      STRICT_SCALA=1
      shift
      ;;
    --out-root)
      OUT_ROOT="$2"
      shift 2
      ;;
    --no-build)
      BUILD_IMAGE=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      CUSTOM_CMD=("$@")
      break
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${#EXPERIMENTS[@]}" -eq 0 ]]; then
  echo "at least one --experiment is required" >&2
  usage >&2
  exit 2
fi

if ! [[ "$MAX_PARALLEL" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid --max-parallel: $MAX_PARALLEL" >&2
  exit 2
fi

if [[ "$BUILD_IMAGE" == "1" ]]; then
  docker build -t "$IMAGE_TAG" "$SCRIPT_DIR"
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
if [[ -n "$OUT_ROOT" ]]; then
  OUT_ROOT="${OUT_ROOT/#\~/$HOME}"
  mkdir -p "$OUT_ROOT"
fi

declare -a pids=()
declare -a labels=()
overall_status=0

wait_for_one() {
  local i pid rc
  if [[ "${#pids[@]}" -eq 0 ]]; then
    return 0
  fi
  pid="${pids[0]}"
  if wait "$pid"; then
    rc=0
  else
    rc=$?
    overall_status=1
  fi
  echo "[done] ${labels[0]} exit=$rc"
  pids=("${pids[@]:1}")
  labels=("${labels[@]:1}")
}

for exp in "${EXPERIMENTS[@]}"; do
  label="${exp%%=*}"
  repo="${exp#*=}"
  if [[ -z "$label" || -z "$repo" || "$label" == "$exp" ]]; then
    echo "invalid --experiment format: $exp (want <label>=<repo_root>)" >&2
    exit 2
  fi
  repo="${repo/#\~/$HOME}"
  if [[ ! -d "$repo" ]]; then
    echo "experiment repo root does not exist: $repo" >&2
    exit 2
  fi

  while [[ "${#pids[@]}" -ge "$MAX_PARALLEL" ]]; do
    wait_for_one
  done

  cmd=(
    "$RUNNER"
    --no-build
    --image "$IMAGE_TAG"
    --memory "$MEMORY_LIMIT"
    --cpus "$CPUS_LIMIT"
    --pids "$PIDS_LIMIT"
    --repo-root "$repo"
    --label "$label"
  )
  if [[ -n "$OUT_ROOT" ]]; then
    cmd+=(--out-root "$OUT_ROOT")
  fi
  if [[ -n "$RUN_REGEX" ]]; then
    cmd+=(--run "$RUN_REGEX")
  fi
  if [[ "$STRICT_SCALA" == "1" ]]; then
    cmd+=(--strict-scala)
  fi
  if [[ "${#CUSTOM_CMD[@]}" -gt 0 ]]; then
    cmd+=(-- "${CUSTOM_CMD[@]}")
  fi

  echo "[start] label=$label repo=$repo"
  "${cmd[@]}" &
  pids+=("$!")
  labels+=("$label")
done

while [[ "${#pids[@]}" -gt 0 ]]; do
  wait_for_one
done

exit "$overall_status"
