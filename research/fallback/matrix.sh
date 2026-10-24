#!/bin/bash
# Two rounds of research/fallback/netem.sh over the slow links.
cd "$(dirname "$0")"
for round in 1 2; do for l in "2 100" "5 10" "5 100" "10 40" "20 100"; do ./netem.sh $l $round; done; done
