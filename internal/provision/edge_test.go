package provision

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

func TestCloudflareToggleKeepsEstablishedDirectConnection(t *testing.T) {
	// An independent listener stands in for the active Xray node. Keep one
	// upgraded connection open across both Caddy configuration changes.
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if rw.Flush() != nil {
			return
		}
		_, _ = io.Copy(conn, rw)
	}))
	defer direct.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(direct.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: node.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatal(response.StatusCode)
	}

	applied := make(chan string, 2)
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/adapt":
			body, _ := io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"apps": map[string]any{"http": string(body)}}})
		case "/load":
			body, _ := io.ReadAll(r.Body)
			applied <- string(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer admin.Close()
	base := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(base, []byte("{$PANEL_HOST} {\n reverse_proxy panel:8080\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(admin.URL, "http://"))
	}}
	defer transport.CloseIdleConnections()
	edge := CloudflareEdge{client: &http.Client{Transport: transport, Timeout: time.Second}, panelHost: "panel.example.com", configPath: base}
	n := core.Node{Host: "192.0.2.2", Domain: "node.example.com", WSPath: "/ws"}
	site := edgeSite{name: edgeHostname(n), node: n, cert: Certificate{Certificate: []byte("isolated-cert"), Key: []byte("isolated-key")}}

	ctx := context.Background()
	for _, enabled := range []bool{true, false} {
		var sites []edgeSite
		if enabled {
			sites = []edgeSite{site}
		}
		if err := edge.load(ctx, sites); err != nil {
			t.Fatal(err)
		}
		config := <-applied
		if !strings.Contains(config, "panel.example.com") || strings.Contains(config, site.name) != enabled {
			t.Fatalf("unexpected edge route state after toggle to %v", enabled)
		}
		if enabled && (!strings.Contains(config, "tls_server_name node.example.com") || !strings.Contains(config, "isolated-cert")) {
			t.Fatal("Caddy must validate upstream TLS and use the dedicated certificate")
		}
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		var echoed [4]byte
		if _, err := io.ReadFull(reader, echoed[:]); err != nil || string(echoed[:]) != "ping" {
			t.Fatalf("direct connection did not survive toggle to %v: %q, %v", enabled, echoed, err)
		}
	}
}
