package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BuildConfig deliberately avoids sniffing and access logs: no destination history.
func BuildConfig(n Node, users []User, creds []Credential, now time.Time) ([]byte, error) {
	active := map[string]bool{}
	for _, u := range users {
		if u.Active(now) {
			active[u.ID] = true
		}
	}
	reality, ws, trojan := []any{}, []any{}, []any{}
	probe := ProbeCredential(n)
	reality = append(reality, map[string]any{"id": probe.UUID, "flow": "xtls-rprx-vision"})
	ws = append(ws, map[string]any{"id": probe.UUID})
	trojan = append(trojan, map[string]any{"password": probe.Password})
	for _, c := range creds {
		if c.NodeID != n.ID || !active[c.UserID] {
			continue
		}
		email := c.UserID
		reality = append(reality, map[string]any{"id": c.UUID, "email": email, "flow": "xtls-rprx-vision", "level": 0})
		ws = append(ws, map[string]any{"id": c.UUID, "email": email, "level": 0})
		trojan = append(trojan, map[string]any{"password": c.Password, "email": email, "level": 0})
	}
	tls := map[string]any{"certificates": []any{map[string]any{"certificateFile": "/etc/rahpanel/tls.crt", "keyFile": "/etc/rahpanel/tls.key"}}, "minVersion": "1.2"}
	inbound := func(tag, protocol string, port int, clients []any, stream any) any {
		settings := map[string]any{"clients": clients}
		if protocol == "vless" {
			settings["decryption"] = "none"
		}
		return map[string]any{"tag": tag, "listen": "0.0.0.0", "port": port, "protocol": protocol, "settings": settings, "streamSettings": stream}
	}
	cfg := map[string]any{
		"log":    map[string]any{"loglevel": "warning", "access": "none"},
		"api":    map[string]any{"tag": "api", "services": []string{"StatsService"}},
		"stats":  map[string]any{},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]bool{"statsUserUplink": true, "statsUserDownlink": true}}},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door", "settings": map[string]string{"address": "127.0.0.1"}},
			inbound("reality", "vless", 443, reality, map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"show": false, "target": n.RealityTarget, "xver": 0, "serverNames": []string{n.RealitySNI}, "privateKey": n.RealityPrivate, "shortIds": []string{n.ShortID}}}),
			inbound("websocket", "vless", 8443, ws, map[string]any{"network": "ws", "security": "tls", "tlsSettings": tls, "wsSettings": map[string]string{"path": n.WSPath}}),
			inbound("trojan", "trojan", 2053, trojan, map[string]any{"network": "tcp", "security": "tls", "tlsSettings": tls}),
		},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]string{"domainStrategy": "UseIP"}}, map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing": map[string]any{"domainStrategy": "IPOnDemand", "rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"},
			map[string]any{"type": "field", "ip": []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4", "::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8"}, "outboundTag": "block"},
		}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// A node-specific service credential is never included in user subscriptions.
func ProbeCredential(n Node) Credential {
	h := sha256.Sum256([]byte("rah-probe:" + n.RealityPrivate))
	return Credential{UUID: fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16]), Password: base64.RawURLEncoding.EncodeToString(h[16:])}
}

func Subscription(nodes []Node, creds []Credential) string {
	lines := []string{}
	for _, n := range nodes {
		if !n.Ready {
			continue
		}
		for _, c := range creds {
			if c.NodeID != n.ID {
				continue
			}
			for _, kind := range n.Verified {
				q := url.Values{}
				scheme, secret, host, port := "vless", c.UUID, n.Domain, 8443
				q.Set("type", "tcp")
				switch kind {
				case "reality":
					host = n.Host
					port = 443
					q.Set("security", "reality")
					q.Set("sni", n.RealitySNI)
					q.Set("pbk", n.RealityPublic)
					q.Set("sid", n.ShortID)
					q.Set("fp", "chrome")
					q.Set("flow", "xtls-rprx-vision")
					q.Set("encryption", "none")
				case "websocket":
					q.Set("type", "ws")
					q.Set("security", "tls")
					q.Set("sni", n.Domain)
					q.Set("host", n.Domain)
					q.Set("path", n.WSPath)
					q.Set("encryption", "none")
				case "trojan":
					scheme = "trojan"
					secret = c.Password
					port = 2053
					q.Set("security", "tls")
					q.Set("sni", n.Domain)
				default:
					continue
				}
				u := url.URL{Scheme: scheme, User: url.User(secret), Host: net.JoinHostPort(host, strconv.Itoa(port)), RawQuery: q.Encode(), Fragment: n.Name + " · " + kind}
				lines = append(lines, u.String())
			}
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
}
