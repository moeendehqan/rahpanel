package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func healthcheck() {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	endpoint := fs.String("url", "http://127.0.0.1:8080/healthz", "readiness endpoint")
	fs.Parse(os.Args[2:])
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := probeHealth(ctx, *endpoint); err != nil {
		fatal("Panel is not ready")
	}
}

func probeHealth(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness status %d", res.StatusCode)
	}
	return nil
}
