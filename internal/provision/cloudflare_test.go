package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestDNSWriteProbe(t *testing.T) {
	for _, tc := range []struct {
		name, zone, wantError                  string
		readStatus, createStatus, deleteStatus int
		methods                                []string
		cancelDuringCreate                     bool
	}{
		{name: "success after browser disconnects", zone: "example.com", methods: []string{"GET", "POST", "DELETE"}, cancelDuringCreate: true},
		{name: "invalid token", readStatus: 403, wantError: "خواندن Zone", methods: []string{"GET"}},
		{name: "unrelated zone", zone: "ample.com", wantError: "دامنهٔ پایه", methods: []string{"GET"}},
		{name: "write rejected", zone: "example.com", createStatus: 403, wantError: "ساخت رکورد TXT را رد کرد", methods: []string{"GET", "POST"}},
		{name: "write outcome unknown", zone: "example.com", createStatus: 502, wantError: "ساخت رکورد TXT تأیید نشد", methods: []string{"GET", "POST"}},
		{name: "delete failed", zone: "example.com", deleteStatus: 500, wantError: "حذف رکورد TXT تأیید نشد", methods: []string{"GET", "POST", "DELETE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var methods []string
			var probe record
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				if r.Header.Get("Authorization") != "Bearer private-token" {
					t.Error("missing token")
				}
				var status int
				var path string
				var result any
				switch r.Method {
				case "GET":
					status, path, result = tc.readStatus, "/zones/zone", map[string]string{"name": tc.zone}
				case "POST":
					if tc.cancelDuringCreate {
						cancel()
					}
					status, path, result = tc.createStatus, "/zones/zone/dns_records", record{ID: "probe-id"}
					if err := json.NewDecoder(r.Body).Decode(&probe); err != nil || probe.Type != "TXT" ||
						!strings.HasPrefix(probe.Name, "_rahpanel-test-") || !strings.HasSuffix(probe.Name, ".nodes.example.com") {
						t.Errorf("invalid probe: %+v, %v", probe, err)
					}
				case "DELETE":
					status, path = tc.deleteStatus, "/zones/zone/dns_records/probe-id"
				}
				if r.URL.Path != path {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if status != 0 {
					w.WriteHeader(status)
				}
				json.NewEncoder(w).Encode(map[string]any{"success": status == 0, "result": result, "errors": "private-token"})
			}))
			dns := DNS{Zone: "zone", Token: "private-token", Base: server.URL}
			err := dns.TestWrite(ctx, "nodes.example.com")
			server.Close()
			if !slices.Equal(methods, tc.methods) {
				t.Fatalf("methods %v, want %v", methods, tc.methods)
			}
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(tc.wantError, "تأیید نشد") && !strings.Contains(err.Error(), probe.Name) {
				t.Fatalf("missing record name in error: %v", err)
			}
		})
	}
}

func TestDNSNeverOverwritesAnUnownedRecord(t *testing.T) {
	modified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			modified = true
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []record{{ID: "existing", Comment: "someone-else"}}})
	}))
	defer server.Close()
	d := DNS{Token: "token", Zone: "zone", Base: server.URL}
	if e := d.EnsureAddress(context.Background(), "n.example.com", "203.0.113.1", "node"); e == nil {
		t.Fatal("unowned DNS record overwritten")
	}
	if modified {
		t.Fatal("mutation attempted")
	}
}
func TestDNSAddressIsAlwaysDNSOnly(t *testing.T) {
	var published record
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authorization")
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []record{}})
		} else {
			json.NewDecoder(r.Body).Decode(&published)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": published})
		}
	}))
	defer server.Close()
	d := DNS{Token: "token", Zone: "zone", Base: server.URL}
	if e := d.EnsureAddress(context.Background(), "n.example.com", "203.0.113.1", "node"); e != nil {
		t.Fatal(e)
	}
	if published.Proxied || published.Comment != "rahpanel:node" || published.Type != "A" {
		t.Fatalf("unsafe record: %+v", published)
	}
}
