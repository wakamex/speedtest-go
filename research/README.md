# Rate measurement research

Evidence for the rate meter, acknowledged-byte upload counting and stopping rule on this branch. [DESIGN.md](DESIGN.md) has the design and results.

## Contents

- `speedtest/testdata/stopping_traces`: 132 fixed-length 15 s traces recorded with master and early stopping turned off, against 8 public servers and 21 [netem](https://man7.org/linux/man-pages/man8/tc-netem.8.html) links.
- `speedtest/testdata/stopping_traces_v2`: 144 traces recorded the same way with this branch, at 8 connections and, for some tests, 32.
- `speedtest/estimator_trace_test.go`: the recorder, `TestRecordEstimatorTraces`, which samples payload, confirmed and acknowledged bytes every 50 ms.
- `speedtest/stopping_replay_test.go` and `speedtest/meter_replay_test.go`: replays of candidate stopping rules and of the meter on the recorded traces.
- `research/pr_tables.py`: prints the accuracy table in the pull request description.
- `research/netem.sh`, `research/netem_matrix.sh`, `research/v2_netem.sh`, `research/v2_real.sh`: record the trace sets. `research/localserver` is the local server behind the netem links.
- `research/conncount`: throughput at 8, 16, 32 and 64 connections against 21 public servers and 30 netem links, with results.
- `research/fallback`: upload on slow netem links for master, this branch, and this branch with acknowledged bytes turned off, with results.

The research commit adds two fields to `DataManager`, `disableEarlyStop` and `settledAt`, that the recorder uses to run every test the full 15 s and note when the rate settled.

## Reproducing

Replay the traces and print the tables, from the repository root:

```sh
go test ./speedtest -run 'TestStoppingReplay|TestMeterReplay' -v
python3 research/pr_tables.py
```

Recording needs Linux. The netem scripts create network namespaces with `unshare -rnm`, so they run as a normal user where unprivileged user namespaces are allowed:

```sh
go build -o research/localserver/localserver ./research/localserver
research/netem_matrix.sh                    # netem traces with the current code
research/v2_real.sh                         # public-server traces
```

The connection-count runs need `go build -o research/conncount/conncount ./research/conncount`, then `research/conncount/real.sh` and `research/conncount/netem_matrix.sh`. The fallback runs need `research/fallback/bin/conncount-master`, `conncount-pr` and `conncount-prfb`, built from `research/conncount/main.go` against master, this branch, and this branch with `tcpAcked` in `speedtest/tcpinfo_linux.go` returning false; then run `research/fallback/matrix.sh`.
