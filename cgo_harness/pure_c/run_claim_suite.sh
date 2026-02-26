#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

RUNS="${RUNS:-5}"
SIZES="${SIZES:-500 2000 10000}"
OUT_DIR="${OUT_DIR:-$HARNESS_DIR/reports}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$OUT_DIR/claim-suite-$STAMP"
RAW_DIR="$RUN_DIR/raw"
DATA_TSV="$RUN_DIR/data.tsv"
REPORT_MD="$RUN_DIR/report.md"

mkdir -p "$RAW_DIR"
printf "size\tengine\tmetric\tns_op\n" >"$DATA_TSV"

calc_full_iters() {
  local size="$1"
  local n=$((1000000 / size))
  if ((n < 100)); then
    n=100
  fi
  echo "$n"
}

calc_inc_iters() {
  local size="$1"
  local n=$((1000000 / size))
  if ((n < 200)); then
    n=200
  fi
  echo "$n"
}

parse_go_bench_ns() {
  local file="$1"
  local bench="$2"
  awk -v b="$bench" '
    $1 ~ ("^" b "(-[0-9]+)?$") {
      for (i = 1; i < NF; i++) {
        if ($(i + 1) == "ns/op") {
          print $i
          exit
        }
      }
    }
  ' "$file"
}

parse_pure_c_ns() {
  local file="$1"
  local key="$2"
  awk -F= -v k="$key" '$1 == k { print $2; exit }' "$file"
}

record_value() {
  local size="$1"
  local engine="$2"
  local metric="$3"
  local value="$4"
  if [[ -z "$value" ]]; then
    echo "missing value: size=$size engine=$engine metric=$metric" >&2
    exit 1
  fi
  printf "%s\t%s\t%s\t%s\n" "$size" "$engine" "$metric" "$value" >>"$DATA_TSV"
}

echo "claim suite output: $RUN_DIR"
echo "runs=$RUNS sizes=$SIZES"

{
  echo "timestamp_utc=$(date -u +%FT%TZ)"
  echo "uname=$(uname -a)"
  echo "go=$(go version)"
  echo "gcc=$(gcc --version | head -n1)"
  if command -v clang >/dev/null 2>&1; then
    echo "clang=$(clang --version | head -n1)"
  fi
} >"$RUN_DIR/system.txt"

for size in $SIZES; do
  full_iters="$(calc_full_iters "$size")"
  inc_iters="$(calc_inc_iters "$size")"
  echo "size=$size full_iters=$full_iters inc_iters=$inc_iters"

  for run in $(seq 1 "$RUNS"); do
    go_file="$RAW_DIR/go_${size}_run${run}.txt"
    cgo_file="$RAW_DIR/cgo_${size}_run${run}.txt"
    pure_c_file="$RAW_DIR/purec_${size}_run${run}.txt"

    (
      cd "$REPO_ROOT"
      GOT_BENCH_FUNC_COUNT="$size" \
        go test . -run '^$' \
          -bench '^(BenchmarkGoParseFull|BenchmarkGoParseIncrementalSingleByteEdit|BenchmarkGoParseIncrementalRandomSingleByteEdit|BenchmarkGoParseIncrementalNoEdit)$' \
          -benchmem -count=1
    ) >"$go_file"

    (
      cd "$HARNESS_DIR"
      GOT_BENCH_FUNC_COUNT="$size" \
        go test . -run '^$' -tags treesitter_c_bench \
          -bench '^(BenchmarkCTreeSitterGoParseFull|BenchmarkCTreeSitterGoParseIncrementalSingleByteEdit|BenchmarkCTreeSitterGoParseIncrementalRandomSingleByteEdit|BenchmarkCTreeSitterGoParseIncrementalNoEdit)$' \
          -benchmem -count=1
    ) >"$cgo_file"

    "$SCRIPT_DIR/run_go_benchmark.sh" "$size" "$full_iters" "$inc_iters" >"$pure_c_file"

    record_value "$size" "gotreesitter" "full" "$(parse_go_bench_ns "$go_file" "BenchmarkGoParseFull")"
    record_value "$size" "gotreesitter" "inc_edit" "$(parse_go_bench_ns "$go_file" "BenchmarkGoParseIncrementalSingleByteEdit")"
    record_value "$size" "gotreesitter" "inc_edit_random" "$(parse_go_bench_ns "$go_file" "BenchmarkGoParseIncrementalRandomSingleByteEdit")"
    record_value "$size" "gotreesitter" "inc_noedit" "$(parse_go_bench_ns "$go_file" "BenchmarkGoParseIncrementalNoEdit")"

    record_value "$size" "cgo" "full" "$(parse_go_bench_ns "$cgo_file" "BenchmarkCTreeSitterGoParseFull")"
    record_value "$size" "cgo" "inc_edit" "$(parse_go_bench_ns "$cgo_file" "BenchmarkCTreeSitterGoParseIncrementalSingleByteEdit")"
    record_value "$size" "cgo" "inc_edit_random" "$(parse_go_bench_ns "$cgo_file" "BenchmarkCTreeSitterGoParseIncrementalRandomSingleByteEdit")"
    record_value "$size" "cgo" "inc_noedit" "$(parse_go_bench_ns "$cgo_file" "BenchmarkCTreeSitterGoParseIncrementalNoEdit")"

    record_value "$size" "pure_c" "full" "$(parse_pure_c_ns "$pure_c_file" "pure_c_full_ns_op")"
    record_value "$size" "pure_c" "inc_edit" "$(parse_pure_c_ns "$pure_c_file" "pure_c_inc_edit_ns_op")"
    record_value "$size" "pure_c" "inc_edit_random" "$(parse_pure_c_ns "$pure_c_file" "pure_c_inc_edit_random_ns_op")"
    record_value "$size" "pure_c" "inc_noedit" "$(parse_pure_c_ns "$pure_c_file" "pure_c_inc_noedit_ns_op")"
  done
done

{
  echo "# Claim Suite Report"
  echo
  echo "- runs per size: \`$RUNS\`"
  echo "- sizes: \`$SIZES\`"
  echo "- raw data: \`$DATA_TSV\`"
  echo
  echo "| size | metric | gotreesitter ns/op (median) | pure C ns/op (median) | cgo ns/op (median) | pure C vs got speedup | cgo vs got speedup |"
  echo "|---:|---|---:|---:|---:|---:|---:|"
  awk '
    BEGIN { FS = "\t" }
    NR == 1 { next }
    {
      key = $1 "|" $2 "|" $3
      vals[key] = vals[key] " " $4
      sizes[$1] = 1
      metrics[$3] = 1
    }
    function median(series, arr, n, i, j, t, m) {
      n = split(series, arr, " ")
      m = 0
      delete b
      for (i = 1; i <= n; i++) {
        if (arr[i] == "") continue
        m++
        b[m] = arr[i] + 0
      }
      for (i = 1; i <= m; i++) {
        for (j = i + 1; j <= m; j++) {
          if (b[j] < b[i]) {
            t = b[i]; b[i] = b[j]; b[j] = t
          }
        }
      }
      if (m == 0) return 0
      if (m % 2 == 1) return b[(m + 1) / 2]
      return (b[m / 2] + b[m / 2 + 1]) / 2
    }
    END {
      metric_order[1] = "full"
      metric_order[2] = "inc_edit"
      metric_order[3] = "inc_edit_random"
      metric_order[4] = "inc_noedit"

      nsize = 0
      for (s in sizes) {
        nsize++
        size_list[nsize] = s + 0
      }
      for (i = 1; i <= nsize; i++) {
        for (j = i + 1; j <= nsize; j++) {
          if (size_list[j] < size_list[i]) {
            t = size_list[i]; size_list[i] = size_list[j]; size_list[j] = t
          }
        }
      }

      for (i = 1; i <= nsize; i++) {
        s = size_list[i]
        for (k = 1; k <= 4; k++) {
          metric = metric_order[k]
          g = median(vals[s "|gotreesitter|" metric])
          p = median(vals[s "|pure_c|" metric])
          c = median(vals[s "|cgo|" metric])
          if (g == 0 || p == 0 || c == 0) continue
          sp = p / g
          sc = c / g
          printf("| %d | %s | %.2f | %.2f | %.2f | %.2fx | %.2fx |\n", s, metric, g, p, c, sp, sc)
        }
      }
    }
  ' "$DATA_TSV"
} >"$REPORT_MD"

echo "report: $REPORT_MD"
