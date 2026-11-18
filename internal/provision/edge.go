package provision

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/store"
)

const edgeConfig = "/run/rah-edge-base/Caddyfile"

var edgeHostPattern = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9.-]+$")
var edgePathPattern = regexp.MustCompile("^/[a-zA-Z0-9/_-]+$")

type CloudflareEdge struct {
	store                                     *store.Store
	client                                    *http.Client
	panelHost, agentHost, originIP, publicURL string
	wake                                      chan struct{}
	mu                                        sync.RWMutex
	statuses                                  map[string]edgeStatus
	certRetry                                 map[string]time.Time
	configPath                                string
	cancel                                    context.CancelFunc
}

type edgeStatus struct {
	key    routeKey
	status core.CloudflareWSStatus
}

type edgeSite struct {
	node core.Node
	name string
	cert Certificate
}

func NewCloudflareEdge(s *store.Store, socket, panelHost, agentURL, originIP, publicURL string) *CloudflareEdge {
	if socket == "" {
		return nil
	}
	agent, _ := url.Parse(agentURL)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &CloudflareEdge{
		store: s, client: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		panelHost: panelHost, agentHost: agent.Hostname(), originIP: originIP, publicURL: publicURL,
		wake: make(chan struct{}, 1), statuses: make(map[string]edgeStatus),
		certRetry: make(map[string]time.Time), configPath: edgeConfig,
	}
}

func edgeHostname(n core.Node) string { return "cf-" + n.Domain }

func (e *CloudflareEdge) Apply(n *core.Node) {
	if e == nil {
		return
	}
	n.CloudflareWS = &core.CloudflareWSStatus{Hostname: edgeHostname(*n)}
	if n.CloudflareWSRequested == nil {
		return
	}
	n.CloudflareWS.Enabled = *n.CloudflareWSRequested
	e.mu.RLock()
	current := e.statuses[n.ID]
	e.mu.RUnlock()
	if current.key == keyFor(*n) && current.status.Enabled == n.CloudflareWS.Enabled {
		*n.CloudflareWS = current.status
	}
	n.CloudflareWS.Ready = n.CloudflareWS.Ready && edgeEligible(*n)
}

func edgeEligible(n core.Node) bool {
	return n.Enabled && n.Ready && time.Since(n.LastSeen) <= 5*time.Minute && slices.Contains(n.Verified, "websocket")
}

func (e *CloudflareEdge) Wake(id string) {
	e.mu.Lock()
	delete(e.statuses, id)
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *CloudflareEdge) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		pass, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.cancel = cancel
		e.mu.Unlock()
		e.reconcile(pass)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-ticker.C:
		}
	}
}

func (e *CloudflareEdge) reconcile(ctx context.Context) {
	nodes, err := e.store.Nodes(ctx)
	if err != nil {
		return
	}
	var cfg Settings
	cfgErr := e.store.GetSetting(ctx, "provider", &cfg)
	dns := DNS{Token: cfg.CloudflareToken, Zone: cfg.ZoneID}
	var sites []edgeSite
	statuses := make(map[string]edgeStatus)
	var candidates []core.Node
	var removals []core.Node
	for _, n := range nodes {
		if n.CloudflareWSRequested == nil {
			continue
		}
		status := core.CloudflareWSStatus{Hostname: edgeHostname(n), Enabled: *n.CloudflareWSRequested}
		switch {
		case !status.Enabled:
			removals = append(removals, n)
		case !edgeEligible(n):
			status.Error = "WebSocket مستقیم نود در دسترس نیست"
		default:
			candidates = append(candidates, n)
		}
		statuses[n.ID] = edgeStatus{key: keyFor(n), status: status}
	}
	setError := func(n core.Node, message string) {
		status := statuses[n.ID]
		status.status.Ready = false
		status.status.Error = message
		statuses[n.ID] = status
	}
	for _, n := range candidates {
		if ctx.Err() != nil {
			return
		}
		site := edgeSite{node: n, name: edgeHostname(n)}
		if cfgErr != nil || site.name == e.panelHost || !edgeHostPattern.MatchString(site.name) || !edgeHostPattern.MatchString(n.Domain) ||
			!strings.HasSuffix(site.name, "."+cfg.Domain) || !edgePathPattern.MatchString(n.WSPath) || net.ParseIP(n.Host) == nil {
			setError(n, "تنظیمات DNS یا WebSocket نود معتبر نیست")
			continue
		}
		var certErr error
		certificateCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		site.cert, certErr = e.certificate(certificateCtx, cfg, site.name)
		cancel()
		if certErr != nil {
			setError(n, "صدور یا بارگذاری گواهی Caddy ناموفق بود")
			continue
		}
		sites = append(sites, site)
	}
	if len(statuses) == 0 || ctx.Err() != nil {
		return
	}
	hostnames := make([]string, 0, len(sites))
	for _, site := range sites {
		hostnames = append(hostnames, site.name)
	}
	var strictErr error
	if cfgErr == nil {
		strictErr = dns.edgeStrict(ctx, hostnames)
	}
	var configured []edgeSite
	if len(sites) > 0 {
		ip, ipErr := e.address(ctx, nodes)
		for _, site := range sites {
			switch {
			case ipErr != nil:
				setError(site.node, "IP عمومی VPS پنل پیدا نشد")
			case strictErr != nil:
				setError(site.node, "Full (strict) یا مجوز Config Rules Edit در Cloudflare را بررسی کنید")
			case dns.ensureAddress(ctx, site.name, ip, "edge-"+site.node.ID, true) != nil:
				setError(site.node, "ساخت رکورد Proxied ناموفق بود یا نام از قبل استفاده شده است")
			default:
				configured = append(configured, site)
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err := e.load(ctx, configured); err != nil {
		for _, site := range configured {
			setError(site.node, "اعمال پیکربندی Caddy ناموفق بود")
		}
		for _, n := range removals {
			setError(n, "حذف مسیر Caddy در انتظار تلاش مجدد است")
		}
	} else {
		for _, n := range removals {
			if cfgErr != nil || dns.removeEdgeAddress(ctx, edgeHostname(n), n.ID) != nil {
				setError(n, "پاک‌سازی DNS در انتظار تلاش مجدد است")
				continue
			}
			if strictErr != nil {
				setError(n, "پاک‌سازی قانون TLS در انتظار تلاش مجدد است")
				continue
			}
			_, _ = e.store.DB.ExecContext(ctx, "UPDATE nodes SET cloudflare_ws=NULL WHERE id=$1 AND cloudflare_ws=false", n.ID)
		}
		e.verify(ctx, configured, statuses, setError)
	}
	e.mu.Lock()
	if ctx.Err() == nil {
		e.statuses = statuses
	}
	e.mu.Unlock()
}

func (e *CloudflareEdge) verify(ctx context.Context, sites []edgeSite, statuses map[string]edgeStatus, setError func(core.Node, string)) {
	if len(sites) == 0 || ctx.Err() != nil {
		return
	}
	binary, err := loadXray(runtime.GOARCH)
	for _, site := range sites {
		if ctx.Err() != nil {
			return
		}
		n := site.node
		route := statuses[n.ID]
		n.CloudflareWS = &route.status
		switch {
		case err != nil:
			setError(n, "فایل Xray برای آزمون مسیر Cloudflare در دسترس نیست")
		case probeTransport(ctx, binary, n, "ws-cloudflare", e.publicURL) != nil:
			setError(n, "آزمون اتصال از Cloudflare به نود ناموفق بود")
		default:
			route.status.Ready = true
			statuses[n.ID] = route
		}
	}
}

func (e *CloudflareEdge) address(ctx context.Context, nodes []core.Node) (string, error) {
	if e.originIP != "" {
		if ip := net.ParseIP(e.originIP); ip != nil {
			return ip.String(), nil
		}
		return "", errors.New("invalid RAH_EDGE_IP")
	}
	for _, n := range nodes {
		if n.SharedHost && n.Enabled && n.Ready {
			return n.Host, nil
		}
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", e.agentHost)
	if err != nil || len(ips) != 1 {
		return "", errors.New("no unique edge IP")
	}
	return ips[0].String(), nil
}

func (e *CloudflareEdge) certificate(ctx context.Context, cfg Settings, hostname string) (Certificate, error) {
	var saved Certificate
	key := "cloudflare-edge-cert:" + hostname
	err := e.store.GetSetting(ctx, key, &saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Certificate{}, err
	}
	if time.Until(saved.Expires) > 30*24*time.Hour {
		return saved, nil
	}
	if time.Now().Before(e.certRetry[hostname]) {
		if time.Now().Before(saved.Expires) {
			return saved, nil
		}
		return Certificate{}, errors.New("certificate issuance backoff")
	}
	issued, err := IssueCertificate(ctx, e.store, cfg, hostname)
	if err != nil && ctx.Err() == nil {
		e.certRetry[hostname] = time.Now().Add(10 * time.Minute)
	}
	if err == nil {
		err = e.store.SetSetting(ctx, key, issued)
	}
	if err == nil {
		delete(e.certRetry, hostname)
		return issued, nil
	}
	if time.Now().Before(saved.Expires) {
		return saved, nil
	}
	return Certificate{}, err
}

func (e *CloudflareEdge) load(ctx context.Context, sites []edgeSite) error {
	base, err := os.ReadFile(e.configPath)
	if err != nil {
		return err
	}
	if !edgeHostPattern.MatchString(e.panelHost) {
		return errors.New("invalid panel hostname")
	}
	var config strings.Builder
	config.WriteString(strings.ReplaceAll(string(base), "{$PANEL_HOST}", e.panelHost))
	var certs []map[string]string
	for _, site := range sites {
		n := site.node
		upstream := net.JoinHostPort(n.Host, strconv.Itoa(n.EffectivePorts().WebSocket))
		fmt.Fprintf(&config, "\n%s {\n @ws {\n  path %s\n  header Upgrade websocket\n }\n route {\n  reverse_proxy @ws https://%s {\n   header_up Host %s\n   transport http {\n    versions 1.1\n    tls_server_name %s\n   }\n   stream_close_delay 5m\n  }\n  respond 404\n }\n}\n", site.name, n.WSPath, upstream, n.Domain, n.Domain)
		certs = append(certs, map[string]string{"certificate": string(site.cert.Certificate), "key": string(site.cert.Key)})
	}
	adapted, err := e.caddy(ctx, "/adapt", "text/caddyfile", []byte(config.String()))
	if err != nil {
		return err
	}
	var result struct{ Result map[string]json.RawMessage }
	if err := json.Unmarshal(adapted, &result); err != nil {
		return err
	}
	if len(certs) != 0 {
		var apps map[string]json.RawMessage
		if err := json.Unmarshal(result.Result["apps"], &apps); err != nil {
			return err
		}
		var tlsApp map[string]json.RawMessage
		if raw := apps["tls"]; raw != nil {
			if err := json.Unmarshal(raw, &tlsApp); err != nil {
				return err
			}
		}
		if tlsApp == nil {
			tlsApp = make(map[string]json.RawMessage)
		}
		tlsApp["certificates"], err = json.Marshal(map[string]any{"load_pem": certs})
		if err != nil {
			return err
		}
		apps["tls"], err = json.Marshal(tlsApp)
		if err != nil {
			return err
		}
		result.Result["apps"], err = json.Marshal(apps)
		if err != nil {
			return err
		}
	}
	body, err := json.Marshal(result.Result)
	if err != nil {
		return err
	}
	_, err = e.caddy(ctx, "/load", "application/json", body)
	return err
}

func (e *CloudflareEdge) caddy(ctx context.Context, path, contentType string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := e.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Caddy %s: HTTP %d", path, response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}
