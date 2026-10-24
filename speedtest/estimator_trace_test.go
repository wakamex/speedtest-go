package speedtest

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const estimatorTraceSchemaVersion = 1

type estimatorTrace struct {
	SchemaVersion       int                    `json:"schema_version"`
	Fixture             string                 `json:"fixture"`
	CapturedAt          time.Time              `json:"captured_at"`
	SpeedtestGoVersion  string                 `json:"speedtest_go_version"`
	Direction           string                 `json:"direction"`
	Server              estimatorTraceServer   `json:"server"`
	Threads             int                    `json:"threads"`
	RequestedDurationMS int64                  `json:"requested_duration_ms"`
	ActualDurationMS    int64                  `json:"actual_duration_ms"`
	SampleIntervalMS    int64                  `json:"sample_interval_ms"`
	Interface           string                 `json:"interface"`
	InterfaceSpeedMbps  int64                  `json:"interface_speed_mbps"`
	FinalPayloadBytes   int64                  `json:"final_payload_bytes"`
	FinalConfirmedBytes int64                  `json:"final_confirmed_bytes"`
	Condition           string                 `json:"condition,omitempty"`
	EarlyStopDisabled   bool                   `json:"early_stop_disabled,omitempty"`
	ProductionStopMS    int64                  `json:"production_stop_ms,omitempty"`
	Samples             []estimatorTraceSample `json:"samples"`
}

type estimatorTraceServer struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Sponsor   string  `json:"sponsor"`
	URL       string  `json:"url"`
	LatencyMS float64 `json:"latency_ms"`
}

type estimatorTraceSample struct {
	ElapsedMS               int64   `json:"elapsed_ms"`
	PayloadBytes            int64   `json:"payload_bytes"`
	ConfirmedBytes          int64   `json:"confirmed_bytes"`
	NICTxBytes              int64   `json:"nic_tx_bytes"`
	NICRxBytes              int64   `json:"nic_rx_bytes"`
	DisplayedBytesPerSecond float64 `json:"displayed_bytes_per_second"`
	ActiveWorkers           int32   `json:"active_workers"`
	TCPAckedBytes           int64   `json:"tcp_acked_bytes,omitempty"`
	TCPReceivedBytes        int64   `json:"tcp_received_bytes,omitempty"`
}

func TestRecordEstimatorTraces(t *testing.T) {
	if os.Getenv("SPEEDTEST_RECORD_ESTIMATOR_TRACES") != "1" {
		t.Skip("set SPEEDTEST_RECORD_ESTIMATOR_TRACES=1 to record real network traces")
	}

	servers := strings.Split(envOrDefault("SPEEDTEST_TRACE_SERVERS", envOrDefault("SPEEDTEST_TRACE_SERVER", "46151")), ",")
	iface := envOrDefault("SPEEDTEST_TRACE_INTERFACE", "enp16s0")
	label := envOrDefault("SPEEDTEST_TRACE_LABEL", time.Now().UTC().Format("20060102T150405Z"))
	threads := envInt(t, "SPEEDTEST_TRACE_THREADS", 8)
	repeats := envInt(t, "SPEEDTEST_TRACE_REPEATS", 1)
	duration := time.Duration(envInt(t, "SPEEDTEST_TRACE_DURATION_SECONDS", 15)) * time.Second
	directions := strings.Split(envOrDefault("SPEEDTEST_TRACE_DIRECTIONS", "download,upload"), ",")
	interval := 50 * time.Millisecond

	for run := 1; run <= repeats; run++ {
		for _, serverID := range servers {
			for _, direction := range directions {
				fixture := fmt.Sprintf("%s-%s-%s-run%d", label, serverID, direction, run)
				t.Run(fixture, func(t *testing.T) {
					trace := recordEstimatorTrace(t, fixture, direction, serverID, iface, threads, duration, interval)
					writeEstimatorTrace(t, trace)
					t.Logf("recorded %s: %.2fs, %.2f Mbps payload, %d samples, production stop %d ms", fixture, float64(trace.ActualDurationMS)/1000, tracePayloadMbps(trace), len(trace.Samples), trace.ProductionStopMS)
				})
			}
		}
	}
}

func recordEstimatorTrace(t *testing.T, fixture, direction, serverID, iface string, threads int, duration, interval time.Duration) estimatorTrace {
	t.Helper()
	client := New(WithUserConfig(&UserConfig{MaxConnections: threads}))
	client.SetCaptureTime(duration)
	client.SetRateCaptureFrequency(interval)
	manager := client.Manager.(*DataManager)
	manager.disableEarlyStop = os.Getenv("SPEEDTEST_TRACE_EARLY_STOP") == "0"
	var server *Server
	var err error
	if localURL := os.Getenv("SPEEDTEST_TRACE_URL"); localURL != "" {
		server = &Server{URL: localURL, ID: serverID, Name: "local", Sponsor: "local", Context: client}
	} else if server, err = client.FetchServerByID(serverID); err != nil {
		t.Fatalf("fetch server %s: %v", serverID, err)
	}
	if err := server.PingTest(nil); err != nil {
		t.Fatalf("ping server %s: %v", serverID, err)
	}

	var displayedBits atomic.Uint64
	callback := func(rate ByteRate) { displayedBits.Store(math.Float64bits(float64(rate))) }
	if direction == "download" {
		client.SetCallbackDownload(callback)
	} else {
		client.SetCallbackUpload(callback)
	}

	startTx := readInterfaceCounter(t, iface, "tx_bytes")
	startRx := readInterfaceCounter(t, iface, "rx_bytes")
	start := time.Now()
	trace := estimatorTrace{
		SchemaVersion:       estimatorTraceSchemaVersion,
		Fixture:             fixture,
		CapturedAt:          start.UTC(),
		SpeedtestGoVersion:  Version(),
		Direction:           direction,
		Threads:             threads,
		RequestedDurationMS: duration.Milliseconds(),
		SampleIntervalMS:    interval.Milliseconds(),
		Interface:           iface,
		InterfaceSpeedMbps:  readInterfaceSpeed(t, iface),
		Condition:           os.Getenv("SPEEDTEST_TRACE_CONDITION"),
		EarlyStopDisabled:   manager.disableEarlyStop,
	}
	trace.Samples = append(trace.Samples, estimatorTraceSample{})

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				trace.Samples = append(trace.Samples, captureEstimatorSample(client, direction, iface, start, now, startTx, startRx, displayedBits.Load()))
			case <-stop:
				return
			}
		}
	}()

	if direction == "download" {
		err = server.DownloadTest()
	} else {
		err = server.UploadTest()
	}
	end := time.Now()
	close(stop)
	<-done
	if err != nil {
		t.Fatalf("%s test: %v", direction, err)
	}
	finalSample := captureEstimatorSample(client, direction, iface, start, end, startTx, startRx, displayedBits.Load())
	if finalSample.ElapsedMS <= trace.Samples[len(trace.Samples)-1].ElapsedMS {
		trace.Samples[len(trace.Samples)-1] = finalSample
	} else {
		trace.Samples = append(trace.Samples, finalSample)
	}
	trace.ActualDurationMS = end.Sub(start).Milliseconds()
	trace.Server = estimatorTraceServer{
		ID:        server.ID,
		Name:      server.Name,
		Sponsor:   server.Sponsor,
		URL:       server.URL,
		LatencyMS: float64(server.Latency) / float64(time.Millisecond),
	}
	stopAt := manager.download.settledAt
	if direction == "upload" {
		stopAt = manager.upload.settledAt
	}
	trace.ProductionStopMS = stopAt.Milliseconds()
	final := trace.Samples[len(trace.Samples)-1]
	trace.FinalPayloadBytes = final.PayloadBytes
	trace.FinalConfirmedBytes = final.ConfirmedBytes
	return trace
}

func captureEstimatorSample(client *Speedtest, direction, iface string, start, now time.Time, startTx, startRx int64, displayedBits uint64) estimatorTraceSample {
	manager := client.Manager.(*DataManager)
	payload := client.GetTotalDownload()
	confirmed := payload
	workers := int32(0)
	if direction == "upload" {
		payload = manager.upload.GetTotalReadVolume()
		confirmed = client.GetTotalUpload()
	}
	acked, _ := manager.upload.acked.total()
	return estimatorTraceSample{
		ElapsedMS:               now.Sub(start).Milliseconds(),
		PayloadBytes:            payload,
		ConfirmedBytes:          confirmed,
		NICTxBytes:              readInterfaceCounterValue(iface, "tx_bytes") - startTx,
		NICRxBytes:              readInterfaceCounterValue(iface, "rx_bytes") - startRx,
		DisplayedBytesPerSecond: math.Float64frombits(displayedBits),
		ActiveWorkers:           workers,
		TCPAckedBytes:           acked,
	}
}

func writeEstimatorTrace(t *testing.T, trace estimatorTrace) {
	t.Helper()
	directory := envOrDefault("SPEEDTEST_TRACE_DIR", filepath.Join("testdata", "estimator_traces"))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create trace directory: %v", err)
	}
	temporary, err := os.CreateTemp(directory, ".trace-*.json")
	if err != nil {
		t.Fatalf("create temporary trace: %v", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(trace); err != nil {
		_ = temporary.Close()
		t.Fatalf("encode trace: %v", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		t.Fatalf("set trace permissions: %v", err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatalf("close trace: %v", err)
	}
	path := filepath.Join(directory, trace.Fixture+".json")
	if err := os.Rename(temporaryName, path); err != nil {
		t.Fatalf("install trace: %v", err)
	}
}

func readEstimatorTrace(t *testing.T, path string) estimatorTrace {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var trace estimatorTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	trace.Samples = coalesceEstimatorSamples(trace.Samples)
	return trace
}

func coalesceEstimatorSamples(samples []estimatorTraceSample) []estimatorTraceSample {
	coalesced := samples[:0]
	for _, sample := range samples {
		if len(coalesced) > 0 && sample.ElapsedMS == coalesced[len(coalesced)-1].ElapsedMS {
			coalesced[len(coalesced)-1] = sample
			continue
		}
		coalesced = append(coalesced, sample)
	}
	return coalesced
}

func readInterfaceCounter(t *testing.T, iface, counter string) int64 {
	t.Helper()
	value := readInterfaceCounterValue(iface, counter)
	if value < 0 {
		t.Fatalf("read %s counter %s", iface, counter)
	}
	return value
}

func readInterfaceCounterValue(iface, counter string) int64 {
	data, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", counter))
	if err != nil {
		return -1
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return -1
	}
	return value
}

func readInterfaceSpeed(t *testing.T, iface string) int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "speed"))
	if err != nil {
		return 0 // loopback and virtual interfaces have no link speed
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatalf("parse %s speed: %v", iface, err)
	}
	return value
}

func tracePayloadMbps(trace estimatorTrace) float64 {
	return float64(trace.FinalPayloadBytes) * 8 / (float64(trace.ActualDurationMS) / 1000) / 1_000_000
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", name, value, err)
	}
	return parsed
}
