#!/bin/bash
# Usage: research/netem.sh RATE_MBIT RTT_MS LABEL
# Records traces against research/localserver across a veth pair between two private
# network namespaces. Each direction has its own netem qdisc: half the RTT and the
# full rate, with a 50 ms drop-tail queue, so data and ACKs never share a queue.
set -euo pipefail
rate=$1 rtt=$2 label=$3
here=$(cd "$(dirname "$0")/.." && pwd)
exec unshare -rnm --fork bash -s "$rate" "$rtt" "$label" "$here" <<'INNER'
set -euo pipefail
rate=$1 rtt=$2 label=$3 here=$4
mount -t sysfs sysfs /sys
half=$(awk "BEGIN{print $rtt/2}")
limit=$(( rate * 1000000 / 8 / 20 / 1500 ))  # 50 ms of queue at the shaped rate
(( limit < 100 )) && limit=100
shape="netem delay ${half}ms rate ${rate}mbit limit $limit"
ip link set lo up
ip link add v0 type veth peer name v1
unshare -n bash -c "
  while ! ip link show v1 >/dev/null 2>&1; do sleep 0.05; done
  ip link set lo up; ip addr add 10.9.0.2/24 dev v1; ip link set v1 up
  tc qdisc add dev v1 root $shape
  exec '$here/research/localserver/localserver' 10.9.0.2:8080" & srv=$!
trap 'kill $srv 2>/dev/null || true' EXIT
sleep 0.2
ip link set v1 netns "$srv"
ip addr add 10.9.0.1/24 dev v0; ip link set v0 up
tc qdisc add dev v0 root $shape
for i in $(seq 50); do (exec 3<>/dev/tcp/10.9.0.2/8080) 2>/dev/null && break; sleep 0.1; done
cd "$here"
SPEEDTEST_RECORD_ESTIMATOR_TRACES=1 SPEEDTEST_TRACE_URL=http://10.9.0.2:8080/upload.php \
SPEEDTEST_TRACE_INTERFACE=v0 SPEEDTEST_TRACE_SERVERS=local \
SPEEDTEST_TRACE_CONDITION="netem ${rate}mbit ${rtt}ms" SPEEDTEST_TRACE_LABEL="$label" \
go test ./speedtest -run '^TestRecordEstimatorTraces$' -count=1 -v -timeout=30m 2>&1 | grep --line-buffered -E "recorded|FAIL|panic|Error|tcpinfo" || true
INNER
