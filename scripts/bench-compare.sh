#!/usr/bin/env bash
# Before/after microbenchmarks for PR impact (mock httptest only).
# Not part of make test / make preflight — run explicitly:
#   ./scripts/bench-compare.sh
#   ./scripts/bench-compare.sh upstream/main HEAD
#   make bench-compare
#
# Stability knobs (env):
#   BENCHTIME  per-run duration (default 300ms)
#   BENCHCOUNT total go-test -count runs (default 4 = 1 warm-up + 3 measured)
#   BENCHWARM  how many leading runs to drop (default 1)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BASE_REF="${1:-upstream/main}"
HEAD_REF="${2:-HEAD}"
BENCHTIME="${BENCHTIME:-300ms}"
BENCHCOUNT="${BENCHCOUNT:-4}"
BENCHWARM="${BENCHWARM:-1}"
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

if ! [[ "$BENCHCOUNT" =~ ^[0-9]+$ ]] || (( BENCHCOUNT < 1 )); then
	echo "BENCHCOUNT must be a positive integer" >&2
	exit 1
fi
if ! [[ "$BENCHWARM" =~ ^[0-9]+$ ]] || (( BENCHWARM >= BENCHCOUNT )); then
	echo "BENCHWARM must be < BENCHCOUNT (got warm=$BENCHWARM count=$BENCHCOUNT)" >&2
	exit 1
fi

TMP=$(mktemp -d)
trap 'git worktree remove --force "$TMP/base" 2>/dev/null || true; git worktree remove --force "$TMP/head" 2>/dev/null || true; rm -rf "$TMP"' EXIT

echo "=== worktrees: base=$BASE_REF head=$HEAD_REF ==="
echo "=== params: benchtime=$BENCHTIME count=$BENCHCOUNT warm-drop=$BENCHWARM (report median of remaining) ==="
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
	( cd "$dir" && go test ./pkg/mock/ -run='^$' -bench="$COMPARE_BENCH" -benchtime="$BENCHTIME" -benchmem -count="$BENCHCOUNT" )
}

BASE_OUT=$TMP/base.out
HEAD_OUT=$TMP/head.out
FEAT_OUT=$TMP/feat.out
run_compare "before (main)" "$TMP/base" | tee "$BASE_OUT"
run_compare "after (PR)" "$TMP/head" | tee "$HEAD_OUT"

echo
echo "=== feature benches (PR only) ==="
( cd "$TMP/head" && go test ./pkg/mock/ -run='^$' -bench="$FEATURE_BENCH" -benchtime="$BENCHTIME" -benchmem -count="$BENCHCOUNT" ) | tee "$FEAT_OUT"

python3 - "$BASE_OUT" "$HEAD_OUT" "$FEAT_OUT" "$BENCHWARM" <<'PY'
import re, sys, statistics
from pathlib import Path

warm = int(sys.argv[4])

def parse_samples(path):
    # name -> list of (ns, B, allocs) in run order
    rows = {}
    for line in Path(path).read_text().splitlines():
        m = re.match(
            r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(\d+(?:\.\d+)?)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op',
            line,
        )
        if not m:
            continue
        name = m.group(1)
        sample = (float(m.group(2)), int(m.group(3)), int(m.group(4)))
        rows.setdefault(name, []).append(sample)
    return rows

def stabilize(samples):
    """Drop warm-up runs; return (median_ns, median_B, median_allocs, n, min_ns, max_ns)."""
    if len(samples) <= warm:
        keep = samples
    else:
        keep = samples[warm:]
    ns = [s[0] for s in keep]
    bs = [s[1] for s in keep]
    als = [s[2] for s in keep]
    return (
        statistics.median(ns),
        int(statistics.median(bs)),
        int(statistics.median(als)),
        len(keep),
        min(ns),
        max(ns),
    )

def fmt_ns(n):
    if n >= 1000:
        return str(int(round(n)))
    return f"{n:.1f}"

base_raw, head_raw, feat_raw = parse_samples(sys.argv[1]), parse_samples(sys.argv[2]), parse_samples(sys.argv[3])
base = {k: stabilize(v) for k, v in base_raw.items()}
head = {k: stabilize(v) for k, v in head_raw.items()}
feat = {k: stabilize(v) for k, v in feat_raw.items()}

names = sorted(set(base) | set(head))
print()
print(f"## Before / after (median of {next(iter(base.values()))[3] if base else '?'} measured runs after {warm} warm-up drop; httptest mock only)")
print()
print("| Benchmark | before ns/op | after ns/op | Δ ns | Δ % | range before | range after | before B/op | after B/op |")
print("|-----------|-------------:|------------:|-----:|----:|-------------:|------------:|------------:|-----------:|")
for n in names:
    if n not in base or n not in head:
        continue
    bn, bb, _, _, bmin, bmax = base[n]
    an, ab, _, _, amin, amax = head[n]
    d = an - bn
    pct = (100.0 * d / bn) if bn else 0.0
    print(
        f"| `{n}` | {fmt_ns(bn)} | {fmt_ns(an)} | {int(round(d)):+d} | {pct:+.1f}% "
        f"| {fmt_ns(bmin)}–{fmt_ns(bmax)} | {fmt_ns(amin)}–{fmt_ns(amax)} | {bb} | {ab} |"
    )

if feat:
    print()
    print(f"## Feature benches (PR only; median after {warm} warm-up drop)")
    print()
    print("| Benchmark | ns/op | B/op | allocs/op | range ns/op |")
    print("|-----------|------:|-----:|----------:|------------:|")
    for n in sorted(feat):
        ns, bop, al, _, mn, mx = feat[n]
        print(f"| `{n}` | {fmt_ns(ns)} | {bop} | {al} | {fmt_ns(mn)}–{fmt_ns(mx)} |")
PY

echo
cp "$BASE_OUT" /tmp/telegym-bench-before.out
cp "$HEAD_OUT" /tmp/telegym-bench-after.out
cp "$FEAT_OUT" /tmp/telegym-bench-feature.out
echo "Copies: /tmp/telegym-bench-{before,after,feature}.out"
echo "Re-run: make bench-compare   (or BENCHCOUNT=6 BENCHWARM=1 ./scripts/bench-compare.sh)"
