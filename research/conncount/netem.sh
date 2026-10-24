#!/bin/bash
# Usage: netem.sh RATE_MBIT RTT_MS LOSS_PERCENT ROUND
# Runs conncount at 8, 16, 32 and 64 connections against research/localserver across a
# veth pair shaped by netem in each direction (half the RTT, the full rate, the given loss).
set -euo pipefail
rate=$1 rtt=$2 loss=$3 round=$4
here=$(cd "$(dirname "$0")/../.." && pwd)
exec unshare -rnm --fork bash -s "$rate" "$rtt" "$loss" "$round" "$here" <<'INNER'
set -euo pipefail
rate=$1 rtt=$2 loss=$3 round=$4 here=$5
half=$(awk "BEGIN{print $rtt/2}")
limit=$(( rate * 1000000 / 8 / 20 / 1500 )); (( limit < 100 )) && limit=100
shape="netem delay ${half}ms rate ${rate}mbit loss ${loss}% limit $limit"
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
for t in $(shuf -e 8 16 32 64); do
  timeout 180 "$here/research/conncount/conncount" -t "$t" -url http://10.9.0.2:8080/upload.php -label "netem-${rate}m-${rtt}ms-loss${loss}-round$round" \
    || echo "{\"threads\":$t,\"error\":\"timeout or crash\",\"label\":\"netem-${rate}m-${rtt}ms-loss${loss}-round$round\"}"
done
INNER
