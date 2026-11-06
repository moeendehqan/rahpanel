package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
