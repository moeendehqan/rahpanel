package core

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// MihomoSubscription converts the same ordered, verified routes used by the
// existing subscription into YAML with JSON flow mappings.
func MihomoSubscription(nodes []Node, creds []Credential) string {
	raw, _ := base64.StdEncoding.DecodeString(subscription(nodes, creds, true))
	if len(raw) == 0 {
		return "proxies: []\nproxy-groups: []\n"
	}
	var out strings.Builder
	names, reality, hysteria, others := []string{}, []string{}, []string{}, []string{}
	out.WriteString("proxies:\n")
	for _, line := range strings.Split(string(raw), "\n") {
		route, err := url.Parse(line)
		if err != nil {
			continue
		}
		q := route.Query()
		port, _ := strconv.Atoi(route.Port())
		name := strconv.Itoa(len(names)+1) + " · " + route.Fragment
		proxy := map[string]any{"name": name, "type": route.Scheme, "server": route.Hostname(), "port": port, "sni": q.Get("sni")}
		if route.Scheme == "hysteria2" {
			proxy["password"] = route.User.Username()
			proxy["skip-cert-verify"] = false
			hysteria = append(hysteria, name)
		} else if route.Scheme == "trojan" {
			proxy["password"] = route.User.Username()
		} else {
			proxy["uuid"] = route.User.Username()
			proxy["tls"] = true
			proxy["servername"] = q.Get("sni")
			delete(proxy, "sni")
			if q.Get("security") == "reality" {
				proxy["flow"] = q.Get("flow")
				proxy["client-fingerprint"] = q.Get("fp")
				proxy["reality-opts"] = map[string]string{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
			} else {
				proxy["network"] = "ws"
				proxy["ws-opts"] = map[string]any{"path": q.Get("path"), "headers": map[string]string{"Host": q.Get("host")}}
			}
		}
		encoded, _ := json.Marshal(proxy)
		out.WriteString("  - " + string(encoded) + "\n")
		names = append(names, name)
		if q.Get("security") == "reality" {
			reality = append(reality, name)
		} else if route.Scheme != "hysteria2" {
			others = append(others, name)
		}
	}
	manual, fallbackNames := names, names
	if len(names) >= 2 {
		if len(reality) > 0 && len(hysteria) > 0 {
			manual = append([]string{"Rah Auto"}, names...)
			fallbackNames = append(append(reality, hysteria...), others...)
		} else {
			manual = append(append([]string(nil), names...), "Rah Auto")
		}
	}
	group, _ := json.Marshal(map[string]any{"name": "Rah", "type": "select", "proxies": manual})
	out.WriteString("proxy-groups:\n  - " + string(group) + "\n")
	if len(names) >= 2 {
		fallback, _ := json.Marshal(map[string]any{
			"name": "Rah Auto", "type": "fallback", "proxies": fallbackNames,
			"url": "https://www.gstatic.com/generate_204", "interval": 60, "timeout": 5000,
		})
		out.WriteString("  - " + string(fallback) + "\n")
	}
	return out.String()
}
