package speedtest

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Replays fixed-duration traces (early stopping disabled) and scores stopping
// rules and rate estimators against each trace's own full-length result.

type replaySeries struct {
	fixture, class string
	t              []float64 // seconds
	b              []float64 // cumulative bytes of the estimator's input signal
	productionStop float64   // seconds; 0 when the Welford rule never fired
	latencyMS      float64
}

// rateBetween is bytes over time between the samples nearest t0 and t1.
func (s replaySeries) rateBetween(t0, t1 float64) float64 {
	i, j := s.index(t0), s.index(t1)
	if j <= i || s.t[j] <= s.t[i] {
		return 0
	}
	return (s.b[j] - s.b[i]) / (s.t[j] - s.t[i])
}

func (s replaySeries) index(at float64) int {
	i := sort.SearchFloat64s(s.t, at)
	if i >= len(s.t) {
		return len(s.t) - 1
	}
	if i > 0 && at-s.t[i-1] < s.t[i]-at {
		return i - 1
	}
	return i
}

// windowStart is where an estimator's window begins for an estimate made at t.
type windowStart func(t float64) float64

var estimators = map[string]windowStart{
	"cumulative": func(t float64) float64 { return 0 },
	"last-2/3":   func(t float64) float64 { return t / 3 },
	"last-1/2":   func(t float64) float64 { return t / 2 },
}

// newey West standard error of the rate over samples (i, j], as in gesso's gs_eta.
func rateStandardError(s replaySeries, i, j int) float64 {
	n := j - i
	if n < 4 {
		return math.Inf(1)
	}
	span := s.t[j] - s.t[i]
	rate := (s.b[j] - s.b[i]) / span
	residuals := make([]float64, n)
	for k := 0; k < n; k++ {
		residuals[k] = (s.b[i+k+1] - s.b[i+k]) - rate*(s.t[i+k+1]-s.t[i+k])
	}
	lags := int(math.Sqrt(float64(n)))
	longRun := 0.0
	for lag := 0; lag <= lags; lag++ {
		c := 0.0
		for k := lag; k < n; k++ {
			c += residuals[k] * residuals[k-lag]
		}
		weight := 1.0
		if lag > 0 {
			weight = 2 * (1 - float64(lag)/float64(lags+1))
		}
		longRun += weight * c / float64(n)
	}
	if longRun < 0 {
		longRun = 0
	}
	return math.Sqrt(float64(n)*longRun) / span
}

type stopRule struct {
	name string
	// stop returns the stop time for a series and estimator.
	stop func(s replaySeries, start windowStart) float64
}

var productionRule = stopRule{"welford (current)", func(s replaySeries, _ windowStart) float64 {
	if s.productionStop == 0 {
		return 15
	}
	return s.productionStop
}}

// confidenceRule stops once the estimator's window has a relative standard error
// within eps/z and its two halves agree within eps, after minimum seconds.
func confidenceRule(eps, z, minimum float64) stopRule {
	name := fmt.Sprintf("se+trend eps=%.1f%% z=%.0f min=%.0fs", eps*100, z, minimum)
	return stopRule{name, func(s replaySeries, start windowStart) float64 {
		for j := range s.t {
			t := s.t[j]
			if t < minimum {
				continue
			}
			t0 := start(t)
			i := s.index(t0)
			rate := s.rateBetween(t0, t)
			if rate <= 0 {
				continue
			}
			mid := (t0 + t) / 2
			trend := math.Abs(s.rateBetween(mid, t)-s.rateBetween(t0, mid)) / rate
			if z*rateStandardError(s, i, j)/rate <= eps && trend <= eps {
				return t
			}
		}
		return 15
	}}
}

func loadReplaySeries(t *testing.T, signal string) []replaySeries {
	paths, _ := filepath.Glob(filepath.Join(envOrDefault("SPEEDTEST_TRACE_DIR", filepath.Join("testdata", "stopping_traces")), "*.json"))
	var out []replaySeries
	for _, path := range paths {
		trace := readEstimatorTrace(t, path)
		if !trace.EarlyStopDisabled || trace.ActualDurationMS < 14500 {
			continue
		}
		tier := "rtt<20ms"
		switch {
		case trace.Server.LatencyMS >= 100:
			tier = "rtt>=100ms"
		case trace.Server.LatencyMS >= 20:
			tier = "rtt20-100ms"
		}
		class := "real " + tier
		if strings.HasPrefix(trace.Fixture, "netem") {
			class = "netem " + tier
		}
		s := replaySeries{fixture: trace.Fixture, class: trace.Direction + " " + class, productionStop: float64(trace.ProductionStopMS) / 1000, latencyMS: trace.Server.LatencyMS}
		for _, sample := range trace.Samples {
			value := sample.PayloadBytes
			if trace.Direction == "upload" {
				switch signal {
				case "confirmed":
					value = sample.ConfirmedBytes
				case "acked":
					value = sample.TCPAckedBytes
				}
			}
			s.t = append(s.t, float64(sample.ElapsedMS)/1000)
			s.b = append(s.b, float64(value))
		}
		out = append(out, s)
	}
	return out
}

type scoredRule struct {
	name      string
	estimator string
	stop      func(s replaySeries) float64
}

func printScores(title string, series []replaySeries, rules []scoredRule, reference func(s replaySeries, estimator string) float64) {
	fmt.Printf("\n## %s (%d traces)\n", title, len(series))
	fmt.Printf("%-34s %-11s %5s %5s %6s %6s %6s %6s\n", "rule", "window", ">3%", ">5%", "p50", "p95", "max", "stop")
	for _, rule := range rules {
		var errs []float64
		stops, over3, over5 := 0.0, 0, 0
		for _, s := range series {
			at := rule.stop(s)
			estimate := s.rateBetween(estimators[rule.estimator](at), at)
			ref := reference(s, rule.estimator)
			e := math.Abs(estimate-ref) / ref
			errs = append(errs, e)
			stops += at
			if e > 0.03 {
				over3++
			}
			if e > 0.05 {
				over5++
			}
		}
		sort.Float64s(errs)
		n := len(errs)
		fmt.Printf("%-34s %-11s %4.0f%% %4.0f%% %5.1f%% %5.1f%% %5.1f%% %5.1fs\n", rule.name, rule.estimator,
			100*float64(over3)/float64(n), 100*float64(over5)/float64(n),
			100*errs[n/2], 100*errs[int(math.Ceil(0.95*float64(n)))-1], 100*errs[n-1], stops/float64(n))
	}
}

func groupSeries(series []replaySeries) (map[string][]replaySeries, []string) {
	groups := map[string][]replaySeries{}
	for _, s := range series {
		groups[s.class] = append(groups[s.class], s)
	}
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return groups, names
}

func TestStoppingReplay(t *testing.T) {
	signal := envOrDefault("SPEEDTEST_REPLAY_UPLOAD_SIGNAL", "acked")
	series := loadReplaySeries(t, signal)
	if len(series) == 0 {
		t.Skip("no fixed-duration traces")
	}
	windows := []string{"cumulative", "last-2/3", "last-1/2"}
	groups, names := groupSeries(series)

	// Question 1: which window reports capacity, on links that are steady after ramp-up.
	// Capacity is the rate over 5-15 s of the same trace and signal.
	capacity := func(s replaySeries, _ string) float64 { return s.rateBetween(5, 15) }
	var fixed []scoredRule
	for _, seconds := range []float64{3, 5, 10, 15} {
		for _, w := range windows {
			at := seconds
			fixed = append(fixed, scoredRule{fmt.Sprintf("fixed %.0fs", seconds), w, func(replaySeries) float64 { return at }})
		}
	}
	for _, name := range names {
		var steady []replaySeries
		for _, s := range groups[name] {
			if strings.Contains(name, "netem") || s.latencyMS < 20 {
				steady = append(steady, s)
			}
		}
		if len(steady) > 0 {
			printScores("capacity: "+name+" steady links, upload signal "+signal+"; error vs rate over 5-15 s", steady, fixed, capacity)
		}
	}

	// Question 2: how far an early stop lands from what the same window reports after 15 s.
	full := func(s replaySeries, estimator string) float64 { return s.rateBetween(estimators[estimator](15), 15) }
	var stopping []scoredRule
	for _, w := range windows {
		stopping = append(stopping, scoredRule{"welford (current)", w, func(s replaySeries) float64 { return productionRule.stop(s, nil) }})
	}
	for _, seconds := range []float64{3, 5, 8, 10} {
		at := seconds
		stopping = append(stopping, scoredRule{fmt.Sprintf("fixed %.0fs", seconds), "last-1/2", func(replaySeries) float64 { return at }})
	}
	for _, eps := range []float64{0.01, 0.015, 0.02, 0.03} {
		for _, minimum := range []float64{2, 3} {
			for _, w := range []string{"last-2/3", "last-1/2"} {
				rule := confidenceRule(eps, 2, minimum)
				start := estimators[w]
				stopping = append(stopping, scoredRule{rule.name, w, func(s replaySeries) float64 { return rule.stop(s, start) }})
			}
		}
	}
	for _, name := range names {
		printScores("early stop: "+name+", upload signal "+signal+"; error vs the same window at 15 s", groups[name], stopping, full)
	}
	_ = os.Stdout.Sync()
}
