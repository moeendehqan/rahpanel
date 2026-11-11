package provision

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"golang.org/x/net/proxy"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// verifyTransport authenticates through the actual public inbound, then fetches
// the panel health endpoint through Xray. An open TCP port alone is insufficient.
func verifyTransport(ctx context.Context, n core.Node, kind, healthURL string) error {
	binary, e := loadXray(runtime.GOARCH)
	if e != nil {
		return e
	}
	return probeTransport(ctx, binary, n, kind, healthURL)
}

func probeTransport(ctx context.Context, binary []byte, n core.Node, kind, healthURL string) error {
	dir, e := os.MkdirTemp("", "rah-probe-*")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	executable := filepath.Join(dir, "xray")
	if e = os.WriteFile(executable, binary, 0700); e != nil {
		return e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	credential := core.ProbeCredential(n)
	ports := n.EffectivePorts()
	stream := map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": n.RealitySNI, "fingerprint": "chrome", "publicKey": n.RealityPublic, "shortId": n.ShortID, "spiderX": "/"}}
	protocol := "vless"
	targetPort := ports.Reality
	user := map[string]any{"id": credential.UUID, "encryption": "none", "flow": "xtls-rprx-vision"}
	settings := map[string]any{}
	if kind == "websocket" {
		targetPort = ports.WebSocket
		user = map[string]any{"id": credential.UUID, "encryption": "none"}
		stream = map[string]any{"network": "ws", "security": "tls", "tlsSettings": map[string]any{"serverName": n.Domain}, "wsSettings": map[string]any{"path": n.WSPath, "headers": map[string]string{"Host": n.Domain}}}
	}
	if kind == "trojan" {
		protocol = "trojan"
		targetPort = ports.Trojan
		settings["servers"] = []any{map[string]any{"address": n.Host, "port": targetPort, "password": credential.Password}}
		stream = map[string]any{"network": "tcp", "security": "tls", "tlsSettings": map[string]string{"serverName": n.Domain}}
	} else {
		settings["vnext"] = []any{map[string]any{"address": n.Host, "port": targetPort, "users": []any{user}}}
	}
	cfg := map[string]any{"log": map[string]string{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]string{"auth": "noauth"}}}, "outbounds": []any{map[string]any{"protocol": protocol, "settings": settings, "streamSettings": stream}}}
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		return e
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, executable, "run", "-config", path)
	if e = cmd.Start(); e != nil {
		return e
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	ready := false
	for i := 0; i < 30; i++ {
		conn, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("probe client did not start")
	}
	socks, e := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: 10 * time.Second})
	if e != nil {
		return e
	}
	dialer, ok := socks.(proxy.ContextDialer)
	if !ok {
		return fmt.Errorf("context-aware SOCKS required")
	}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return fetchProbeHealth(c, httpClient, healthURL)
}

func fetchProbeHealth(ctx context.Context, client *http.Client, healthURL string) error {
	req, e := http.NewRequestWithContext(ctx, "GET", healthURL+"/healthz", nil)
	if e != nil {
		return e
	}
	res, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("authenticated probe failed")
	}
	defer res.Body.Close()
	var body struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4096)).Decode(&body); e != nil || res.StatusCode != 200 || body.Service != "rahpanel" || body.Status != "ok" {
		return fmt.Errorf("probe destination did not respond correctly")
	}
	return nil
}
