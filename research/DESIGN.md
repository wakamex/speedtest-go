# Rate measurement and stopping: end-state design

Question: what should speedtest-go measure, report, and stop on, if the whole rate algorithm can be replaced?

Short answer: the replacement has three parts. Upload counts bytes the server's TCP stack has acknowledged rather than bytes in finished HTTP requests. The reported rate drops the first third of the test. The test stops when that rate's standard error and trend are both within 1%, between 4 and 15 seconds. On steady links this stops at 4 to 6 seconds instead of 10 and lands closer to the link's steady rate than the current code, and on unsteady links it runs the full 15 seconds. A fourth problem is outside the estimator: upload worker control collapses at high round-trip times (RTT) and limits what any estimator can see.

How it was tested: 132 fixed-length 15 s traces were recorded with early stopping disabled, 48 against 8 public Ookla servers from 4 ms to 450 ms RTT, and 84 against a local server behind a [netem](https://man7.org/linux/man-pages/man8/tc-netem.8.html) link from 5 Mbit to 1 Gbit at 10, 40 and 100 ms RTT. Each candidate rule was replayed on every trace.

## Upload byte signal

Upload bytes currently count only when a whole 800 kB request is confirmed by an HTTP 200. On slow links this makes the measured rate zero for most of the test and lumpy afterwards. In a 10 Mbit, 40 ms trace, confirmed bytes stayed at zero for the first 4 s, then grew in 0 to 16 Mbps steps per second, while the wire carried a flat 10 Mbps.

The per-connection count of bytes the peer has acknowledged (Linux `TCP_INFO` `tcpi_bytes_acked`) matched the interface counter to the second in the same trace. The recorder reads it through a wrapper around the HTTP transport's dialer.

Upload result from confirmed bytes over a 15 s test, compared with acknowledged bytes over 5 to 15 s:

| Link | Error of the confirmed-byte result |
|---|---|
| 5 Mbit, 10 to 100 ms | 33% to 55% low |
| 10 Mbit, 10 to 100 ms | 5% to 22% low |
| 20 to 300 Mbit, 10 to 40 ms | 0% to 5% low |
| 1 Gbit, 10 ms | under 1% |

Platform availability: Linux reports acknowledged bytes directly. macOS reports send-buffer occupancy (`TCP_CONNECTION_INFO` `tcpi_snd_sbbytes`, wrapped by `golang.org/x/sys/unix`), so acknowledged bytes are bytes written minus bytes still buffered. Windows has `SIO_TCP_INFO`, which `x/sys/windows` does not wrap. Elsewhere the signal falls back to confirmed bytes.

## Reported rate window

The cumulative average from the first byte carries TCP slow start for the whole test. On netem links, where capacity is fixed, the error against the steady 5 to 15 s rate was:

| Link | Window | 5 s | 10 s | 15 s |
|---|---|---|---|---|
| Download, 100 ms RTT | cumulative | 6.9% median, 24.9% max | 3.4%, 12.6% | 2.3%, 8.3% |
| Download, 100 ms RTT | last two thirds | 0.1%, 7.1% | 0.0%, 1.6% | 0.0%, 0.0% |
| Download, 20 to 100 ms | cumulative | 1.9%, 6.1% | 1.0%, 3.1% | 0.6%, 2.0% |
| Download, 20 to 100 ms | last two thirds | 0.0%, 0.2% | 0.0%, 0.1% | 0.0%, 0.0% |
| Download and upload, under 20 ms | either | at most 1.0% | at most 0.5% | at most 0.3% |

Dropping the first third keeps the window proportional to the test, so it needs no ramp-up detector. Dropping the first half gave the same capacity error, but worse early-stop tails on real uploads: at eps 2%, 8% of those traces landed over 3% from the full-length result, against 4% for the last two thirds.

## Stopping rule

Candidate: at each 50 ms tick after a minimum time, compute the rate over the last two thirds of the elapsed time, its Newey-West standard error over the 50 ms byte increments (Bartlett weights, sqrt(n) lags), and the relative difference between the two halves of that window. Stop when 2 standard errors and the trend are both within eps of the rate, or at 15 s. A zero rate never satisfies the rule.

Score: how far the estimate at the stop lands from the same window at 15 s, the result a full-length test would have reported. Selected rows for eps 1.5% and a 3 s minimum, against the current Welford rule (the implementation later raised the minimum to 4 s; see Implementation results):

| Traces | Rule | Mean stop | Over 3% | Max error |
|---|---|---|---|---|
| Real, under 20 ms, upload (9) | current | 10.1 s | 0% | 0.7% |
| Real, under 20 ms, upload (9) | candidate | 3.0 s | 0% | 1.9% |
| Real, under 20 ms, download (9) | current | 10.0 s | 0% | 3.0% |
| Real, under 20 ms, download (9) | candidate | 9.6 s | 22% | 6.0% |
| Real, 20 to 100 ms, both (12) | current | 11.6 s | 17% | 11.6% |
| Real, 20 to 100 ms, both (12) | candidate | 15.0 s | 0% | 0% |
| Real, 100 ms and over, both (18) | current | 14.1 s | 11% | 5.8% |
| Real, 100 ms and over, both (18) | candidate | 15.0 s | 0% | 0% |
| Netem, under 100 ms, both (54) | current | 11.2 s | 0% | 2.5% |
| Netem, under 100 ms, both (54) | candidate | 3.1 s | 4% | 5.7% |
| Netem, 100 ms, download (16) | current | 10.4 s | 0% | 2.3% |
| Netem, 100 ms, download (16) | candidate | 4.6 s | 6% | 3.5% |
| Netem, 100 ms, upload (14) | current | 14.0 s | 14% | 9.7% |
| Netem, 100 ms, upload (14) | candidate | 9.9 s | 7% | 15.8% |

The candidate stops early where the rate is steady and runs to 15 s where it is not. Its misses are on links whose rate changed after the stop, such as the Toronto download, whose one-second rate moved between 1,043 and 1,576 Mbps. No stopping rule can foresee such a change. The current rule scores lower on those few traces because it runs longer and averages from the first byte.

The thresholds were chosen on the same traces they are scored on. The grid had 16 settings, and eps from 1% to 2% gave similar results, so the choice is not sensitive.

## Upload worker control at high RTT

At 100 ms RTT, upload throughput falls during the test on a clean netem link: 370 Mbps in the second second to 40 Mbps by the end on 1 Gbit, and 40 to about 15 Mbps on 50 Mbit. The active worker count fell from 8 to 2 or 3 over the same time. `adaptUploadWorkers` drops workers when its one-second confirmed rate falls 15% below the best seen, or when bytes read but not confirmed exceed 2 MiB per worker. At high bandwidth-delay products both conditions trigger without any congestion: confirmations are lumpy, and the send buffers legitimately hold many megabytes. Real far-server uploads behave the same way: Sydney fell from 132 to 13 Mbps as its workers went from 8 to 1, and Frankfurt fell from 124 to 11 Mbps with 2 workers before recovering to 89 Mbps with 6.

With upload measured from acknowledged bytes, requests no longer need to finish to be counted, so the controller is removed and upload keeps its connections for the whole test.

## Implementation

This branch implements the design in three commits on top of master, followed by one commit of research tooling:

- `speedtest/internal/meter.go` holds the rate window, the standard error and the settling test, with a 4 s minimum, a 1% tolerance and the 15 s capture time as the maximum. `DLSpeed`, `ULSpeed`, the live callbacks, `GetEWMADownloadRate` and `GetEWMAUploadRate` read it. The `Manager` interface and method names are unchanged.
- `speedtest/conn.go` and `speedtest/tcpinfo_*.go` read acknowledged upload bytes. They count only on connections the client's own transport dialed to the server itself, so proxies and custom `http.Client`s, other platforms, Linux before 4.1 and Windows before 10 1703 fall back to confirmed bytes. `GetTotalUpload` returns the bytes the upload rate is measured from.
- `adaptUploadWorkers` is removed. Upload uses 8 connections and download one per CPU, as on master, and `-t` sets both.

The minimum is 4 s rather than 3 s. Bell Ottawa (54836) bursts up to 5% above its steady download rate for the first 3 s, and with a 3 s minimum two of its three runs stopped inside the burst and reported 4.3% and 6.0% high. With 4 s that row's worst is 1.9%, steady links stop 1 s later, and no row gets worse.

The tolerance is 1% rather than 1.5%. A 1 Gbit, 100 ms netem download ran 811, 907, 941, 916 and 927 Mbps in its first five seconds, then 956 from 6 s on. At 1.5% it stopped at 4.9 s and reported 3.5% low; at 1% it waited until 13.9 s and landed 0.2% off, at a cost of about 0.2 s on the average stop across both trace sets.

## Results

Acknowledged bytes match bytes written once the peer has read them all, on Linux, on Windows over a network path, and on a macOS 26 arm64 GitHub Actions runner. Windows loopback overstates them, and on a lossy path to Sydney with about 7% retransmissions Windows ended 1.4% above the bytes written, because its retransmission counter leaves some retransmissions out.

A slow-reading local server, where no upload request completes during the test, reports N/A (-1) on master and, with this branch, a rate within 20% of what the server read, on Linux and macOS.

Accuracy, with each version replayed on its own recordings: master on the 132 traces in `speedtest/testdata/stopping_traces`, and this branch on the 116 eight-connection traces in `speedtest/testdata/stopping_traces_v2`, against the same 8 servers and 21 netem links. Each result is scored against the rate its test carried from 5 to 15 s, so this measures how well each version reports the traffic it saw. When a test runs the full 15 s the branch's result is that rate by construction. Errors are signed, negative when the result is too low. `research/pr_tables.py` prints this table:

| Traces (master / branch) | master: median error | master: worst error | master: average stop | branch: median error | branch: worst error | branch: average stop |
|---|---|---|---|---|---|---|
| Netem download (42 / 42) | -0.9% | -1.8% | 10.2 s | 0.0% | +0.9% | 4.5 s |
| Netem upload (42 / 42) | -6.1% | -96.9% | 12.8 s | 0.0% | -1.0% | 7.0 s |
| Servers under 20 ms, download (9 / 6) | -0.1% | +4.0% | 10.1 s | 0.0% | -0.5% | 12.0 s |
| Servers under 20 ms, upload (9 / 6) | -3.9% | -4.6% | 10.1 s | +0.1% | -0.6% | 4.4 s |
| Servers 20 ms and over, download (15 / 10) | +0.7% | +14.0% | 12.7 s | ran to 15 s | ran to 15 s | 15.0 s |
| Servers 20 ms and over, upload (15 / 10) | -5.0% | -26.8% | 13.4 s | ran to 15 s | ran to 15 s | 15.0 s |

Master's -96.9% is a 5 Mbit, 10 ms upload where it reported 0.15 Mbps because almost no request finished. Master stopped exactly at its 10.05 s minimum on 78 of its 132 traces and ran the full 15 s on 34; the branch ran the full 15 s on 35 of 116, including every server 20 ms and farther.

Removing `adaptUploadWorkers` does not hurt the paths that count confirmed bytes. With acknowledged bytes turned off (`research/fallback`), upload on netem links measured 3.2 to 4.8 Mbps on 5 Mbit against master's 1.5 to 1.8, 9.6 on 10 Mbit against 7.5, and 17.6 to 18.4 on 20 Mbit against 13.8 to 16.1. On 2 Mbit both report N/A.

`GetTotalUpload` used to count confirmed bytes, which read 0 when no request finishes. Bytes read from request bodies are no substitute, because they include data still in send buffers: with 64 connections on a 2 Mbit, 100 ms netem link a test read 8.7 MB from request bodies while the server acknowledged 1.7 MB, about 2 Mbit over its 7.5 s.

End to end with the command-line tool on a 32-CPU machine (32 download connections, 8 upload), four runs against Rogers Ottawa took 13.8 to 22.3 s instead of master's 23.2 to 23.6 s, with the same results (1,717 to 1,745 against 1,714 to 1,724 Mbps down, 1,067 to 1,078 against 1,042 to 1,047 Mbps up). Upload settled at 4 s every time; download settled in three of the four runs. Against Superloop Sydney, upload measured 108 Mbps against master's 28, and 62 to 70 in three more runs.

## Connection count

Far servers cap each connection's rate, so throughput on long paths grows almost in proportion to the connection count. `research/conncount` ran the library at 8, 16, 32 and 64 connections, in shuffled order, twice against each of 21 public servers and 30 netem links (2, 5, 20, 100 and 1,000 Mbit; 10, 100 and 250 ms; 0% and 1% loss). Four servers (76445, 14229, 22288, 60433) failed at every count before transferring anything and are left out. Median gain over 8 connections across the other 17 servers:

| Connections | Download | Upload |
|---|---|---|
| 16 | 1.32 times | 1.21 times |
| 32 | 1.71 times | 1.81 times |
| 64 | 2.17 times | 2.81 times |

At 64, upload reached the 1,070 Mbps line rate on five servers 36 to 92 ms away (Teksavvy Toronto, Denver, Halifax, Vancouver, Dallas AT&T), and Sydney still gained over 32 (182 to 266 against 98 to 170 Mbps). On 1% loss netem links throughput scales the same way (1 Gbit, 10 ms: 136 Mbps with 8, 956 with 64). On servers under 20 ms and links that 8 connections fill, the rate is the same at every count, and 64 never gave a lower rate than 8 beyond run-to-run noise. On 2 and 5 Mbit links upload settles later with more connections (2 Mbit, 100 ms: 5 s with 8, 14.8 s with 64) at the same rate. Pinned to one CPU core, 64 connections reached 1,735 / 1,077 Mbps to Rogers Ottawa using 19% of the core and 21 MB of memory, against 16% and 16 MB with 8.

The defaults stay at master's because a higher count multiplies the load on volunteer-hosted servers, which these tests cannot measure. A fixed 8 for both directions was also tried and rejected: on a 32-CPU machine it lowered Sydney download from 323 to 129 Mbps, and Rogers download varied too much with 8 connections to settle, so the test took 22.0 to 25.2 s.

## Not yet measured

- Live display behavior of the two-thirds window during the first second.
- Wi-Fi and mobile links, where the 1% tolerance may rarely be met and tests would run the full 15 s.
- The load a higher default connection count would put on public servers.
