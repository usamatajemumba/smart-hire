package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// MaybeRunHealthcheck lets the binary act as its own Docker healthcheck.
// Distroless images have no shell or curl, so docker-compose runs
// `/app healthcheck`, which calls GET /healthz on the local server and exits
// 0 (healthy) or 1 (unhealthy). It returns normally when that argument is absent.
func MaybeRunHealthcheck(addr string) {
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		return
	}
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + host + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck status:", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}
