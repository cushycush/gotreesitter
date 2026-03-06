#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="$SCRIPT_DIR/run_parity_in_docker.sh"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

IMAGE_TAG="gotreesitter/cgo-harness:go1.24-local"
MEMORY_LIMIT="8g"
CPUS_LIMIT="4"
PIDS_LIMIT="4096"
OUT_ROOT=""
LABEL="grammargen-real-corpus"
PROFILE="aggressive"
MAX_CASES="25"
MAX_GRAMMARS="12"
SEED_DIR=""
CONTAINER_SEED_DIR=""
OFFLINE=0
BUILD_IMAGE=1

usage() {
  cat <<'USAGE'
Usage: run_grammargen_real_corpus_in_docker.sh [options]

Run grammargen real-corpus parity in an isolated Docker container using
cgo_harness/docker/run_parity_in_docker.sh as the execution harness.

Options:
  --repo-root <path>      Repository/worktree root mounted at /workspace
  --image <tag>           Docker image tag (default: gotreesitter/cgo-harness:go1.24-local)
  --memory <limit>        Container memory limit (default: 8g)
  --cpus <count>          CPU limit (default: 4)
  --pids <count>          PID limit (default: 4096)
  --out-root <path>       Artifact output root (optional)
  --label <name>          Run label suffix (default: grammargen-real-corpus)
  --profile <name>        Real-corpus profile: smoke|balanced|aggressive (default: aggressive)
  --max-cases <n>         Max eligible samples per grammar (default: 25)
  --max-grammars <n>      Max grammars to exercise (0 = unlimited, default: 12)
  --seed-dir <path>       Host seed directory under repo root with grammar repos to copy into /tmp/grammar_parity
  --offline               Do not attempt network cloning; require --seed-dir
  --no-build              Skip docker image build
  -h, --help              Show this help

Notes:
  - The container seeds `/tmp/grammar_parity` from `--seed-dir` when provided.
  - Unless `--offline` is set, it also bootstraps a deterministic subset of
    grammar repos from upstream Git remotes.
  - It then runs:
      go test ./grammargen -run '^TestMultiGrammarImportRealCorpusParity$' -count=1 -v
  - This wrapper sets:
      GTS_GRAMMARGEN_REAL_CORPUS_ENABLE=1
      GTS_GRAMMARGEN_REAL_CORPUS_ALLOW_PARTIAL=1
      GTS_GRAMMARGEN_REAL_CORPUS_FLOORS_PATH=/tmp/real_corpus_parity_floors.json
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      REPO_ROOT="$2"
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
    --out-root)
      OUT_ROOT="$2"
      shift 2
      ;;
    --label)
      LABEL="$2"
      shift 2
      ;;
    --profile)
      PROFILE="$2"
      shift 2
      ;;
    --max-cases)
      MAX_CASES="$2"
      shift 2
      ;;
    --max-grammars)
      MAX_GRAMMARS="$2"
      shift 2
      ;;
    --seed-dir)
      SEED_DIR="$2"
      shift 2
      ;;
    --offline)
      OFFLINE=1
      shift
      ;;
    --no-build)
      BUILD_IMAGE=0
      shift
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

if [[ ! -x "$RUNNER" ]]; then
  echo "runner script is missing or not executable: $RUNNER" >&2
  exit 2
fi

REPO_ROOT="${REPO_ROOT/#\~/$HOME}"
if [[ ! -d "$REPO_ROOT" ]]; then
  echo "repo root does not exist: $REPO_ROOT" >&2
  exit 2
fi
REPO_ROOT="$(cd "$REPO_ROOT" && pwd)"

case "$PROFILE" in
  smoke|balanced|aggressive)
    ;;
  *)
    echo "invalid --profile: $PROFILE (expected smoke|balanced|aggressive)" >&2
    exit 2
    ;;
esac

if ! [[ "$MAX_CASES" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid --max-cases: $MAX_CASES" >&2
  exit 2
fi

if [[ "$MAX_GRAMMARS" != "0" ]] && ! [[ "$MAX_GRAMMARS" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid --max-grammars: $MAX_GRAMMARS" >&2
  exit 2
fi

if [[ -n "$SEED_DIR" ]]; then
  SEED_DIR="${SEED_DIR/#\~/$HOME}"
  if [[ ! -d "$SEED_DIR" ]]; then
    echo "seed dir does not exist: $SEED_DIR" >&2
    exit 2
  fi
  SEED_DIR="$(cd "$SEED_DIR" && pwd)"
  if [[ "$SEED_DIR" == "$REPO_ROOT" ]]; then
    echo "seed dir must be a subdirectory under repo root, not the repo root itself" >&2
    exit 2
  fi
  case "$SEED_DIR" in
    "$REPO_ROOT"/*)
      CONTAINER_SEED_DIR="/workspace/${SEED_DIR#"$REPO_ROOT"/}"
      ;;
    *)
      echo "seed dir must be under repo root so it is visible inside the container: $SEED_DIR" >&2
      exit 2
      ;;
  esac
fi

if [[ "$OFFLINE" == "1" && -z "$CONTAINER_SEED_DIR" ]]; then
  echo "--offline requires --seed-dir under repo root" >&2
  exit 2
fi

read -r -d '' CUSTOM_CMD <<EOF2 || true
set -euo pipefail
export PATH=/usr/local/go/bin:\$PATH
mkdir -p /tmp/grammar_parity

SEED_DIR_IN_CONTAINER="$CONTAINER_SEED_DIR"
OFFLINE_MODE="$OFFLINE"

if [[ -n "\$SEED_DIR_IN_CONTAINER" && -d "\$SEED_DIR_IN_CONTAINER" ]]; then
  for src in "\$SEED_DIR_IN_CONTAINER"/*; do
    if [[ ! -d "\$src" ]]; then
      continue
    fi
    name="\$(basename "\$src")"
    rm -rf "/tmp/grammar_parity/\$name"
    cp -a "\$src" "/tmp/grammar_parity/\$name"
  done
fi

clone_repo() {
  local name="\$1"
  local url="\$2"
  local dest="/tmp/grammar_parity/\$name"
  if [[ -d "\$dest/.git" ]]; then
    git -C "\$dest" fetch --depth=1 origin
    git -C "\$dest" reset --hard FETCH_HEAD
  else
    rm -rf "\$dest"
    git clone --depth=1 "\$url" "\$dest"
  fi
}

if [[ "\$OFFLINE_MODE" != "1" ]]; then
  # Deterministic subset with mature real-world corpora used by importParityGrammars.
  clone_repo json https://github.com/tree-sitter/tree-sitter-json.git
  clone_repo css https://github.com/tree-sitter/tree-sitter-css.git
  clone_repo html https://github.com/tree-sitter/tree-sitter-html.git
  clone_repo graphql https://github.com/tree-sitter/tree-sitter-graphql.git
  clone_repo toml https://github.com/tree-sitter/tree-sitter-toml.git
  clone_repo dockerfile https://github.com/camdencheek/tree-sitter-dockerfile.git
fi

if ! find /tmp/grammar_parity -mindepth 1 -maxdepth 1 -type d | grep -q .; then
  echo "no grammar repos available under /tmp/grammar_parity after seed/bootstrap" >&2
  exit 2
fi

cd /workspace
/usr/bin/time -v env \
  GTS_GRAMMARGEN_REAL_CORPUS_ENABLE=1 \
  GTS_GRAMMARGEN_REAL_CORPUS_ROOT=/tmp/grammar_parity \
  GTS_GRAMMARGEN_REAL_CORPUS_PROFILE=$PROFILE \
  GTS_GRAMMARGEN_REAL_CORPUS_MAX_CASES=$MAX_CASES \
  GTS_GRAMMARGEN_REAL_CORPUS_MAX_GRAMMARS=$MAX_GRAMMARS \
  GTS_GRAMMARGEN_REAL_CORPUS_ALLOW_PARTIAL=1 \
  GTS_GRAMMARGEN_REAL_CORPUS_FLOORS_PATH=/tmp/real_corpus_parity_floors.json \
  go test ./grammargen -run '^TestMultiGrammarImportRealCorpusParity$' -count=1 -v
EOF2

CMD=(
  "$RUNNER"
  --image "$IMAGE_TAG"
  --repo-root "$REPO_ROOT"
  --memory "$MEMORY_LIMIT"
  --cpus "$CPUS_LIMIT"
  --pids "$PIDS_LIMIT"
  --label "$LABEL"
)
if [[ "$BUILD_IMAGE" == "0" ]]; then
  CMD+=(--no-build)
fi
if [[ -n "$OUT_ROOT" ]]; then
  CMD+=(--out-root "$OUT_ROOT")
fi
CMD+=(-- "$CUSTOM_CMD")

"${CMD[@]}"
