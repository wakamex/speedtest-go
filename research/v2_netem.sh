#!/bin/bash
cd "$(dirname "$0")/.."
export SPEEDTEST_TRACE_DIR=testdata/stopping_traces_v2 LABEL_DATE=v2
SPEEDTEST_TRACE_THREADS=8 research/netem_matrix.sh
SPEEDTEST_TRACE_THREADS=32 RTTS=100 SPEEDTEST_TRACE_DIRECTIONS=upload research/netem_matrix.sh
