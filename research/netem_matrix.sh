#!/bin/bash
# Records the slow-link matrix: every rate x RTT, both directions, two repeats.
set -u
cd "$(dirname "$0")/.."
export SPEEDTEST_TRACE_EARLY_STOP=0 SPEEDTEST_TRACE_DIR=${SPEEDTEST_TRACE_DIR:-testdata/stopping_traces} SPEEDTEST_TRACE_REPEATS=${SPEEDTEST_TRACE_REPEATS:-2}
for rate in ${RATES:-5 10 20 50 100 300 1000}; do
  for rtt in ${RTTS:-10 40 100}; do
    research/netem.sh "$rate" "$rtt" "netem-${rate}m-${rtt}ms-t${SPEEDTEST_TRACE_THREADS:-8}-${LABEL_DATE:-20261019}"
  done
done
