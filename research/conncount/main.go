// Command conncount runs one download and one upload test with a fixed connection count
// and prints the result as a JSON line.
//
//	conncount -t 32 -server 46151
//	conncount -t 32 -url http://10.9.0.2:8080/upload.php -label netem-10m-100ms
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

type result struct {
	Label         string  `json:"label"`
	Server        string  `json:"server"`
	Name          string  `json:"name"`
	LatencyMS     float64 `json:"latency_ms"`
	Threads       int     `json:"threads"`
	DownloadMbps  float64 `json:"download_mbps"`
	DownloadSec   float64 `json:"download_s"`
	DownloadMB    float64 `json:"download_mb"`
	DownloadError string  `json:"download_error,omitempty"`
	UploadMbps    float64 `json:"upload_mbps"`
	UploadSec     float64 `json:"upload_s"`
	UploadMB      float64 `json:"upload_confirmed_mb"`
	UploadError   string  `json:"upload_error,omitempty"`
	Error         string  `json:"error,omitempty"`
	At            string  `json:"at"`
}

func main() {
	threads := flag.Int("t", 8, "connections")
	serverID := flag.String("server", "", "speedtest.net server ID")
	url := flag.String("url", "", "upload.php URL of a local server")
	label := flag.String("label", "", "label for the result")
	flag.Parse()

	r := result{Label: *label, Server: *serverID, Threads: *threads, At: time.Now().UTC().Format(time.RFC3339)}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(r) }()

	client := speedtest.New(speedtest.WithUserConfig(&speedtest.UserConfig{MaxConnections: *threads}))
	var server *speedtest.Server
	var err error
	if *url != "" {
		server = &speedtest.Server{URL: *url, ID: "local", Name: "local", Context: client}
	} else if server, err = client.FetchServerByID(*serverID); err != nil {
		r.Error = fmt.Sprint("fetch: ", err)
		return
	}
	r.Name = server.Name + ", " + server.Sponsor
	if err := server.PingTest(nil); err != nil {
		r.Error = fmt.Sprint("ping: ", err)
		return
	}
	r.LatencyMS = float64(server.Latency) / float64(time.Millisecond)

	start := time.Now()
	if err := server.DownloadTest(); err != nil {
		r.DownloadError = err.Error()
	}
	r.DownloadSec = time.Since(start).Seconds()
	r.DownloadMbps = server.DLSpeed.Mbps()
	r.DownloadMB = float64(client.GetTotalDownload()) / 1e6

	start = time.Now()
	if err := server.UploadTest(); err != nil {
		r.UploadError = err.Error()
	}
	r.UploadSec = time.Since(start).Seconds()
	r.UploadMbps = server.ULSpeed.Mbps()
	r.UploadMB = float64(client.GetTotalUpload()) / 1e6
}
