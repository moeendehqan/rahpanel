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
	raw, _ := base64.StdEncoding.DecodeString(Subscription(nodes, creds))
	if len(raw) == 0 {
		return "proxies: []\nproxy-groups: []\n"
	}
	var out strings.Builder
	names := []string{}
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
		if route.Scheme == "trojan" {
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
	}
	group, _ := json.Marshal(map[string]any{"name": "Rah", "type": "select", "proxies": names})
	out.WriteString("proxy-groups:\n  - " + string(group) + "\n")
	return out.String()
}
