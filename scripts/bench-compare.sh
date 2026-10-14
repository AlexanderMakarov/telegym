#!/usr/bin/env bash
# Before/after microbenchmarks for PR impact (mock httptest only).
# Not part of make test / make preflight — run explicitly:
#   ./scripts/bench-compare.sh
#   ./scripts/bench-compare.sh upstream/main HEAD
#   make bench-compare
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BASE_REF="${1:-upstream/main}"
HEAD_REF="${2:-HEAD}"
BENCHTIME="${BENCHTIME:-300ms}"
COMPARE_BENCH='BenchmarkPRImpact_'
FEATURE_BENCH='BenchmarkFeature_'

need_ref() {
	git rev-parse -q --verify "$1^{commit}" >/dev/null || {
		echo "missing ref $1 (try: git fetch upstream)" >&2
		exit 1
	}
}

need_ref "$BASE_REF"
need_ref "$HEAD_REF"

TMP=$(mktemp -d)
trap 'git worktree remove --force "$TMP/base" 2>/dev/null || true; git worktree remove --force "$TMP/head" 2>/dev/null || true; rm -rf "$TMP"' EXIT

echo "=== worktrees: base=$BASE_REF head=$HEAD_REF ==="
git worktree add --detach "$TMP/base" "$BASE_REF" >/dev/null
git worktree add --detach "$TMP/head" "$HEAD_REF" >/dev/null

# Harness may be uncommitted; always overlay from the working tree so both
# sides run the same BenchmarkPRImpact_* names, and head also gets Feature_*.
cp pkg/mock/perf_compare_test.go "$TMP/base/pkg/mock/perf_compare_test.go"
cp pkg/mock/perf_compare_test.go "$TMP/head/pkg/mock/perf_compare_test.go"
cp pkg/mock/perf_bench_test.go "$TMP/head/pkg/mock/perf_bench_test.go"

run_compare() {
	local label=$1 dir=$2
	echo "=== $label ($dir) ===" >&2
	( cd "$dir" && go test ./pkg/mock/ -run='^$' -bench="$COMPARE_BENCH" -benchtime="$BENCHTIME" -benchmem -count=1 )
}

BASE_OUT=$TMP/base.out
HEAD_OUT=$TMP/head.out
FEAT_OUT=$TMP/feat.out
run_compare "before (main)" "$TMP/base" | tee "$BASE_OUT"
run_compare "after (PR)" "$TMP/head" | tee "$HEAD_OUT"

echo
echo "=== feature benches (PR only) ==="
( cd "$TMP/head" && go test ./pkg/mock/ -run='^$' -bench="$FEATURE_BENCH" -benchtime="$BENCHTIME" -benchmem -count=1 ) | tee "$FEAT_OUT"

python3 - "$BASE_OUT" "$HEAD_OUT" "$FEAT_OUT" <<'PY'
import re, sys
from pathlib import Path

def parse(path):
    rows = {}
    for line in Path(path).read_text().splitlines():
        m = re.match(
            r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(\d+(?:\.\d+)?)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op',
            line,
        )
        if m:
            name = m.group(1)
            rows[name] = (float(m.group(2)), int(m.group(3)), int(m.group(4)))
    return rows

def fmt_ns(n):
    if n >= 1000:
        return str(int(round(n)))
    return f"{n:.1f}"

base, head, feat = parse(sys.argv[1]), parse(sys.argv[2]), parse(sys.argv[3])
names = sorted(set(base) | set(head))
print()
print("## Before / after (same machine, httptest mock only)")
print()
print("| Benchmark | before ns/op | after ns/op | Δ ns | before B/op | after B/op |")
print("|-----------|-------------:|------------:|-----:|------------:|-----------:|")
for n in names:
    if n not in base or n not in head:
        print(f"| `{n}` | {'—' if n not in base else fmt_ns(base[n][0])} | {'—' if n not in head else fmt_ns(head[n][0])} | — | — | — |", file=sys.stderr)
        continue
    bn, bb, _ = base[n]
    an, ab, _ = head[n]
    print(f"| `{n}` | {fmt_ns(bn)} | {fmt_ns(an)} | {int(round(an-bn)):+d} | {bb} | {ab} |")

if feat:
    print()
    print("## Feature benches (PR only)")
    print()
    print("| Benchmark | ns/op | B/op | allocs/op |")
    print("|-----------|------:|-----:|----------:|")
    for n in sorted(feat):
        ns, bop, al = feat[n]
        print(f"| `{n}` | {fmt_ns(ns)} | {bop} | {al} |")
PY

echo
echo "Raw outputs kept until exit: $BASE_OUT  $HEAD_OUT  $FEAT_OUT"
echo "Re-run ./scripts/bench-compare.sh (or make bench-compare) to refresh."
# Hold temp dir briefly? Actually trap removes it — copy summary to cwd instead.
cp "$BASE_OUT" /tmp/telegym-bench-before.out
cp "$HEAD_OUT" /tmp/telegym-bench-after.out
cp "$FEAT_OUT" /tmp/telegym-bench-feature.out
echo "Copies: /tmp/telegym-bench-{before,after,feature}.out"
