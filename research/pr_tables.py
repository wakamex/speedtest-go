"""Prints the accuracy table in the pull request description.

Each version is replayed on its own recordings: master on speedtest/testdata/stopping_traces,
this branch on the 8-connection traces in speedtest/testdata/stopping_traces_v2. A result is
scored against the rate its test carried from 5 to 15 s. Run from the repository root:

    python3 research/pr_tables.py
"""
import bisect
import collections
import glob
import json
import math
import os
import statistics

MIN_STOP, TOLERANCE, CAP = 4.0, 0.01, 15.0


def near(T, x):
    """Index of the sample nearest to time x."""
    i = bisect.bisect_left(T, x)
    if i == len(T) or (i > 0 and x - T[i - 1] < T[i] - x):
        i -= 1
    return i


def meter_stop(T, B):
    """Replays internal.Meter: the stop time and the rate over the last two thirds."""
    for j in range(1, len(T)):
        t = T[j]
        if t < MIN_STOP:
            continue
        i = near(T, t / 3)
        n = j - i
        if n < 4:
            continue
        rate = (B[j] - B[i]) / (T[j] - T[i])
        if rate <= 0:
            continue
        m = near(T, (T[i] + T[j]) / 2)
        if abs((B[j] - B[m]) / (T[j] - T[m]) - (B[m] - B[i]) / (T[m] - T[i])) > TOLERANCE * rate:
            continue
        res = [(B[i + k + 1] - B[i + k]) - rate * (T[i + k + 1] - T[i + k]) for k in range(n)]
        lags, long_run = int(math.sqrt(n)), 0.0
        for lag in range(lags + 1):
            c = sum(res[k] * res[k - lag] for k in range(lag, n))
            long_run += (1 if lag == 0 else 2 * (1 - lag / (lags + 1))) * c / n
        if 2 * math.sqrt(n * max(long_run, 0)) / (T[j] - T[i]) <= TOLERANCE * rate:
            return t, rate
    j = len(T) - 1
    i = near(T, T[j] / 3)
    return T[j], (B[j] - B[i]) / (T[j] - T[i])


def score(directory, version):
    rows = collections.defaultdict(list)
    for path in sorted(glob.glob(os.path.join(directory, "*.json"))):
        name = os.path.basename(path)
        if "-t32-" in name:
            continue
        trace = json.load(open(path))
        samples, upload = trace["samples"], trace["direction"] == "upload"
        T = [s["elapsed_ms"] / 1000 for s in samples]
        B = [s.get("tcp_acked_bytes", 0) if upload else s["payload_bytes"] for s in samples]
        ref = (B[near(T, 15)] - B[near(T, 5)]) / (T[near(T, 15)] - T[near(T, 5)])
        if version == "master":
            stop = trace.get("production_stop_ms", 0) / 1000 or CAP
            result = samples[near(T, stop)]["displayed_bytes_per_second"]
        else:
            stop, result = meter_stop(T, B)
        netem = name.startswith("netem")
        latency = float(name.split("-")[2][:-2]) if netem else trace["server"]["latency_ms"]
        kind = "Netem" if netem else ("Servers under 20 ms" if latency < 20 else "Servers 20 ms and over")
        rows[f"{kind}, {trace['direction']}"].append(((result - ref) / ref, min(stop, CAP)))
    return rows


def pct(x):
    return f"{100 * x:+.1f}%".replace("+0.0%", "0.0%").replace("-0.0%", "0.0%")


master = score("speedtest/testdata/stopping_traces", "master")
branch = score("speedtest/testdata/stopping_traces_v2", "branch")
print("| Traces (master / this PR) | master: median error | master: worst error | master: average stop "
      "| this PR: median error | this PR: worst error | this PR: average stop |")
print("|---|---|---|---|---|---|---|")
for row in ["Netem, download", "Netem, upload", "Servers under 20 ms, download", "Servers under 20 ms, upload",
            "Servers 20 ms and over, download", "Servers 20 ms and over, upload"]:
    cells = []
    for rows in (master, branch):
        errors = sorted(e for e, _ in rows[row])
        cells += [pct(errors[len(errors) // 2]), pct(max(errors, key=abs)),
                  f"{statistics.mean(s for _, s in rows[row]):.1f} s"]
    print(f"| {row} ({len(master[row])} / {len(branch[row])}) | " + " | ".join(cells) + " |")
