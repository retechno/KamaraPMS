package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// healthcheck is `api healthcheck [-ready]`: it asks the running server on this machine whether it is alive (/healthz) or ready for traffic (/readyz) and exits 0 or 1.
// The runtime image has no shell, no curl and no wget, so the container health check of Docker is this command. It reads only PMS_HTTP_ADDR, so it needs no secret.
//
// /healthz answers "the process runs" and never looks at the database: a restart would not help a database that is down. /readyz answers "send me traffic": the database
// answers and the schema has the migrations of this release. Use /healthz to decide whether to restart a container and /readyz to decide whether to route to it.
func healthcheck(args []string, getenv func(string) string, out io.Writer) int {
	path := "/healthz"
	for _, a := range args {
		switch a {
		case "-ready", "--ready":
			path = "/readyz"
		default:
			_, _ = fmt.Fprintf(out, "healthcheck: unknown argument %q (use -ready)\n", a)
			return 2
		}
	}
	addr := strings.TrimSpace(getenv("PMS_HTTP_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	url, err := healthURL(addr, path)
	if err != nil {
		_, _ = fmt.Fprintf(out, "healthcheck: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_, _ = fmt.Fprintf(out, "healthcheck: %v\n", err)
		return 2
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(out, "healthcheck: %s: %v\n", path, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(out, "healthcheck: %s answered %d\n", path, resp.StatusCode)
		return 1
	}
	return 0
}

// healthURL turns the listen address into the address to call on this machine: a wildcard host (":8080", "0.0.0.0:8080", "[::]:8080") is called as localhost.
func healthURL(listen, path string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("PMS_HTTP_ADDR %q is not host:port", listen)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path, nil
}

func healthcheckMain() {
	os.Exit(healthcheck(os.Args[2:], os.Getenv, os.Stderr))
}
