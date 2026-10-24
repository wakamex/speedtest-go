// Command localserver serves the speedtest.net HTTP endpoints speedtest-go uses
// (latency.txt, random{N}x{N}.jpg, upload.php) for traces under netem shaping.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

var block = make([]byte, 64*1024)

func main() {
	addr := "127.0.0.1:8080"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	http.HandleFunc("/latency.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "test=test\n")
	})
	http.HandleFunc("/upload.php", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		_, _ = fmt.Fprintf(w, "size=%d", n)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var size int
		if _, err := fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/random"), "%dx", &size); err != nil {
			http.NotFound(w, r)
			return
		}
		total := int64(size) * int64(size) * 2 // about the size of speedtest.net's random JPEGs
		w.Header().Set("Content-Length", fmt.Sprint(total))
		for total > 0 {
			n := int64(len(block))
			if n > total {
				n = total
			}
			if _, err := w.Write(block[:n]); err != nil {
				return
			}
			total -= n
		}
	})
	log.Fatal(http.ListenAndServe(addr, nil))
}
