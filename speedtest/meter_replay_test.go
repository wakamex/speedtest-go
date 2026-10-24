package speedtest

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/showwin/speedtest-go/speedtest/internal"
)

// Replays fixed-duration traces recorded with the meter through internal.Meter and scores where
// it would have stopped against the full 15 s result and against the steady 5-15 s rate.
func TestMeterReplay(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(envOrDefault("SPEEDTEST_TRACE_DIR", filepath.Join("testdata", "stopping_traces_v2")), "*.json"))
	type result struct {
		stop, vsFull, vsSteady float64
		recordedStop           float64
	}
	groups := map[string][]result{}
	for _, path := range paths {
		trace := readEstimatorTrace(t, path)
		if !trace.EarlyStopDisabled || trace.ActualDurationMS < 14500 {
			continue
		}
		var full internal.Meter
		var at internal.Meter
		var times, values []float64
		for _, sample := range trace.Samples {
			value := sample.PayloadBytes
			if trace.Direction == "upload" {
				value = sample.TCPAckedBytes
			}
			times = append(times, float64(sample.ElapsedMS)/1000)
			values = append(values, float64(value))
		}
		stop, stopRate := 15.0, math.NaN()
		for i, seconds := range times {
			full.Add(seconds, int64(values[i]))
			if math.IsNaN(stopRate) {
				at.Add(seconds, int64(values[i]))
				if seconds >= minCaptureTime.Seconds() && at.Settled(settledWithin) {
					stop, stopRate = seconds, at.Rate()
				}
			}
		}
		if math.IsNaN(stopRate) {
			stopRate = full.Rate()
		}
		steady := rateBetweenSeconds(times, values, 5, 15)
		latency := trace.Server.LatencyMS
		var rate, rtt float64
		if _, err := fmt.Sscanf(trace.Fixture, "netem-%fm-%fms", &rate, &rtt); err == nil {
			latency = rtt // configured; a slow link inflates the measured ping
		}
		tier := "rtt<20ms"
		switch {
		case latency >= 100:
			tier = "rtt>=100ms"
		case latency >= 20:
			tier = "rtt20-100ms"
		}
		source := strings.SplitN(trace.Fixture, "-", 2)[0]
		threads := fmt.Sprintf("t%d", trace.Threads)
		key := fmt.Sprintf("%s %s %s %s", trace.Direction, source, tier, threads)
		groups[key] = append(groups[key], result{stop, math.Abs(stopRate-full.Rate()) / full.Rate(), math.Abs(stopRate-steady) / steady, float64(trace.ProductionStopMS) / 1000})
	}
	if len(groups) == 0 {
		t.Skip("no meter traces")
	}
	var keys []string
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Printf("%-34s %3s %6s %6s | vs 15 s: %5s %6s %6s | vs 5-15 s: %5s %6s | %s\n", "traces", "n", "stop", "max", ">3%", "p95", "max", ">3%", "max", "replay=recorded")
	for _, key := range keys {
		rs := groups[key]
		var stops, full, steady []float64
		over3Full, over3Steady, agree := 0, 0, 0
		for _, r := range rs {
			stops, full, steady = append(stops, r.stop), append(full, r.vsFull), append(steady, r.vsSteady)
			if r.vsFull > 0.03 {
				over3Full++
			}
			if r.vsSteady > 0.03 {
				over3Steady++
			}
			recorded := r.recordedStop
			if recorded == 0 {
				recorded = 15
			}
			if math.Abs(recorded-r.stop) <= 0.1 {
				agree++
			} else if testing.Verbose() {
				fmt.Printf("  %s: replay stop %.2fs, recorded %.2fs\n", key, r.stop, recorded)
			}
		}
		sort.Float64s(full)
		sort.Float64s(steady)
		sort.Float64s(stops)
		n := len(rs)
		mean := 0.0
		for _, s := range stops {
			mean += s / float64(n)
		}
		p95 := full[int(math.Ceil(0.95*float64(n)))-1]
		fmt.Printf("%-34s %3d %5.1fs %5.1fs | %13.0f%% %5.1f%% %5.1f%% | %10.0f%% %5.1f%% | %d/%d\n", key, n, mean, stops[n-1],
			100*float64(over3Full)/float64(n), 100*p95, 100*full[n-1], 100*float64(over3Steady)/float64(n), 100*steady[n-1], agree, n)
	}
}

func rateBetweenSeconds(times, values []float64, t0, t1 float64) float64 {
	nearest := func(at float64) int {
		i := sort.SearchFloat64s(times, at)
		if i == len(times) || (i > 0 && at-times[i-1] < times[i]-at) {
			i--
		}
		return i
	}
	i, j := nearest(t0), nearest(t1)
	return (values[j] - values[i]) / (times[j] - times[i])
}
