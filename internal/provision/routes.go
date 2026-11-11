package provision

import (
	"context"
	"log"
	"net/http"
	"runtime"
	"slices"
	"time"

	"github.com/rahpanel/rahpanel/internal/core"
)

var routeKinds = []string{"reality", "websocket", "trojan"}

type routeKey struct {
	id, host, domain, publicKey, sni, shortID, wsPath string
	ports                                             core.NodePorts
}

func keyFor(n core.Node) routeKey {
	return routeKey{n.ID, n.Host, n.Domain, n.RealityPublic, n.RealitySNI, n.ShortID, n.WSPath, n.EffectivePorts()}
}

type routeSample struct {
	core.RouteQuality
	passes, failures int
}

func (q *routeSample) record(ok bool, elapsed time.Duration) {
	q.CheckedAt = time.Now()
	if ok {
		q.failures = 0
		q.passes++
		if q.passes >= 2 {
			q.Published, q.Weak = true, false
		}
		ms := max(int64(1), elapsed.Milliseconds())
		if q.LatencyMS == 0 {
			q.LatencyMS = ms
		} else {
			q.LatencyMS = (3*q.LatencyMS + ms) / 4
		}
	} else {
		q.passes = 0
		q.failures++
		if q.failures >= 3 {
			q.Weak = true
		}
	}
}

func (w *Worker) ApplyRouteQuality(n *core.Node) {
	if w == nil || !n.Enabled || !n.Ready {
		return
	}
	w.routeMu.RLock()
	defer w.routeMu.RUnlock()
	for kind, sample := range w.routes[keyFor(*n)] {
		if time.Since(sample.CheckedAt) > 20*time.Minute {
			continue
		}
		if n.RouteQuality == nil {
			n.RouteQuality = make(map[string]core.RouteQuality)
			n.Verified = slices.Clone(n.Verified)
		}
		n.RouteQuality[kind] = sample.RouteQuality
		if sample.Published && !slices.Contains(n.Verified, kind) {
			n.Verified = append(n.Verified, kind)
		}
	}
}

func (w *Worker) MonitorRoutes(ctx context.Context, publicURL string) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkRoutes(ctx, publicURL)
		}
	}
}

func (w *Worker) checkRoutes(ctx context.Context, publicURL string) {
	nodes, err := w.Store.Nodes(ctx)
	if err != nil {
		log.Printf("Route monitor: cannot list nodes: %v", err)
		return
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	binary, err := loadXray(runtime.GOARCH)
	if err != nil {
		log.Printf("Route monitor: %v", err)
		return
	}
	active := make(map[routeKey]bool)
	for _, n := range nodes {
		if ctx.Err() != nil {
			return
		}
		if !n.Enabled || !n.Ready || time.Since(n.LastSeen) > 5*time.Minute {
			continue
		}
		key := keyFor(n)
		active[key] = true
		if fetchProbeHealth(ctx, client, publicURL) != nil {
			continue
		}
		for _, kind := range routeKinds {
			start := time.Now()
			err := probeTransport(ctx, binary, n, kind, publicURL)
			if ctx.Err() != nil {
				return
			}
			elapsed := time.Since(start)
			if err != nil && fetchProbeHealth(ctx, client, publicURL) != nil {
				break
			}
			w.routeMu.Lock()
			if w.routes == nil {
				w.routes = make(map[routeKey]map[string]routeSample)
			}
			if w.routes[key] == nil {
				w.routes[key] = make(map[string]routeSample)
			}
			sample := w.routes[key][kind]
			if time.Since(sample.CheckedAt) > 20*time.Minute {
				sample = routeSample{}
			}
			sample.record(err == nil, elapsed)
			w.routes[key][kind] = sample
			w.routeMu.Unlock()
		}
	}
	w.routeMu.Lock()
	defer w.routeMu.Unlock()
	for key := range w.routes {
		if !active[key] {
			delete(w.routes, key)
		}
	}
}
