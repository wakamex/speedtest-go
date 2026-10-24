#!/bin/bash
cd "$(dirname "$0")"
for round in 1 2; do
  for rate in 2 5 20 100 1000; do
    for rtt in 10 100 250; do
      for loss in 0 1; do
        ./netem.sh "$rate" "$rtt" "$loss" "$round" >> results/netem.jsonl
      done
    done
  done
done
