package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMihomoFallback(t *testing.T) {
	node := Node{ID: "n", Name: "node", Domain: "n.example.com", Host: "203.0.113.1", Ready: true}
	creds := []Credential{{NodeID: "n", UUID: "123", Password: "secret"}}
	for _, routes := range [][]string{nil, {"reality"}, {"reality", "trojan"}} {
		node.Verified = routes
		output := MihomoSubscription([]Node{node}, creds)
		if len(routes) == 0 {
			if output != "proxies: []\nproxy-groups: []\n" {
				t.Fatalf("unexpected empty subscription: %s", output)
			}
			continue
		}
		proxies, groups, _ := strings.Cut(output, "proxy-groups:\n")
		var names []string
		for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(proxies, "proxies:\n")), "\n") {
			var proxy struct{ Name string }
			if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "- ")), &proxy); err != nil {
				t.Fatal(err)
			}
			names = append(names, proxy.Name)
		}
		if len(names) != len(routes) {
			t.Fatalf("unexpected routes: %s", output)
		}
		lines := strings.Split(strings.TrimSpace(groups), "\n")
		if len(lines) != len(routes) {
			t.Fatalf("unexpected groups: %s", groups)
		}
		for i, line := range lines {
			var group struct {
				Name, Type, URL   string
				Interval, Timeout int
				Proxies           []string
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "- ")), &group); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				want := append([]string(nil), names...)
				if len(routes) > 1 {
					want = append(want, "Rah Auto")
				}
				if group.Name != "Rah" || group.Type != "select" || !reflect.DeepEqual(group.Proxies, want) {
					t.Fatalf("unexpected manual group: %+v", group)
				}
			} else if group.Name != "Rah Auto" || group.Type != "fallback" || !reflect.DeepEqual(group.Proxies, names) ||
				group.URL != "https://www.gstatic.com/generate_204" || group.Interval != 60 || group.Timeout != 5000 {
				t.Fatalf("unexpected fallback group: %+v", group)
			}
		}
	}
}
