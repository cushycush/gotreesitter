#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PURE_OUT="$(mktemp)"
GO_OUT="$(mktemp)"
trap 'rm -f "$PURE_OUT" "$GO_OUT"' EXIT

"$SCRIPT_DIR/run_matrix.sh" | tee "$PURE_OUT"
echo
(
  cd "$REPO_ROOT"
  go test ./grammars -run '^$' \
    -bench '^(BenchmarkParse_(C|Go|Java|HTML|Lua|TOML|YAML))$' \
    -benchmem -count=1
) | tee "$GO_OUT"

echo
echo "head-to-head summary (lower ns/op is better):"
awk '
  BEGIN {
    printf "%-8s %14s %14s %12s\n", "lang", "pure_c_ns/op", "got_ns/op", "speedup";
  }
  FILENAME == ARGV[1] {
    if (match($0, /lang=([^ ]+) .*ns_op=([0-9.]+)/, m)) {
      c[m[1]] = m[2];
    }
    next;
  }
  FILENAME == ARGV[2] {
    if ($1 ~ /^BenchmarkParse_/) {
      lang = $1;
      sub(/^BenchmarkParse_/, "", lang);
      sub(/-.*/, "", lang);
      lang = tolower(lang);
      for (i = 1; i < NF; i++) {
        if ($(i+1) == "ns/op") {
          g[lang] = $i;
          break;
        }
      }
    }
    next;
  }
  END {
    order[1] = "c";
    order[2] = "go";
    order[3] = "java";
    order[4] = "html";
    order[5] = "lua";
    order[6] = "toml";
    order[7] = "yaml";
    for (i = 1; i <= 7; i++) {
      l = order[i];
      if (!(l in c) || !(l in g) || c[l] == 0 || g[l] == 0) {
        continue;
      }
      ratio = c[l] / g[l];
      printf "%-8s %14.2f %14.2f %11.2fx\n", l, c[l], g[l], ratio;
    }
  }
' "$PURE_OUT" "$GO_OUT"
