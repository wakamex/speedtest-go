#!/bin/bash
# Two rounds over all servers; each server runs every connection count in a random order.
cd "$(dirname "$0")"
servers="54836 46151 32632 15038 73620 11583 76445 14229 22288 69099 8862 60433 1782 4243 57176 44081 61933 14979 48463 65184 21439"
for round in 1 2; do
  for s in $servers; do
    for t in $(shuf -e 8 16 32 64); do
      timeout 180 ./conncount -t "$t" -server "$s" -label "real-round$round" >> results/real.jsonl || echo "{\"server\":\"$s\",\"threads\":$t,\"error\":\"timeout or crash\",\"label\":\"real-round$round\"}" >> results/real.jsonl
    done
  done
done
