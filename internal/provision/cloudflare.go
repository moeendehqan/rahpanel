package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DNS struct {
	Token, Zone string
	Client      *http.Client
	Base        string
}
type record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Comment string `json:"comment,omitempty"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func (d DNS) call(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	var e error
	if body != nil {
		payload, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	base := d.Base
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	req, e := http.NewRequestWithContext(ctx, method, base+"/zones/"+url.PathEscape(d.Zone)+path, bytes.NewReader(payload))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+d.Token)
	req.Header.Set("Content-Type", "application/json")
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("Cloudflare request failed")
	}
	defer res.Body.Close()
	var response struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&response); e != nil {
		return fmt.Errorf("invalid Cloudflare response")
	}
	if res.StatusCode/100 != 2 || !response.Success {
		return fmt.Errorf("Cloudflare rejected request (HTTP %d); verify token permissions and zone", res.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(response.Result, out)
	}
	return nil
}
func (d DNS) EnsureAddress(ctx context.Context, domain, ip, nodeID string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid IP")
	}
	kind := "A"
	if parsed.To4() == nil {
		kind = "AAAA"
	}
	var records []record
	if e := d.call(ctx, "GET", "/dns_records?type="+kind+"&name="+url.QueryEscape(domain), nil, &records); e != nil {
		return e
	}
	wanted := record{Type: kind, Name: domain, Content: ip, Comment: "rahpanel:" + nodeID, TTL: 60, Proxied: false}
	if len(records) > 0 {
		if len(records) != 1 || records[0].Comment != wanted.Comment {
			return fmt.Errorf("DNS record exists and is not owned by this node")
		}
		return d.call(ctx, "PUT", "/dns_records/"+url.PathEscape(records[0].ID), wanted, nil)
	}
	return d.call(ctx, "POST", "/dns_records", wanted, nil)
}
func (d DNS) TXT(ctx context.Context, name, value string) (string, error) {
	var r record
	e := d.call(ctx, "POST", "/dns_records", record{Type: "TXT", Name: name, Content: value, TTL: 60, Comment: "rahpanel:acme"}, &r)
	return r.ID, e
}
func (d DNS) Delete(ctx context.Context, id string) error {
	return d.call(ctx, "DELETE", "/dns_records/"+url.PathEscape(id), nil, nil)
}
func waitTXT(ctx context.Context, name, value string) error {
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", "1.1.1.1:53")
	}}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		values, e := resolver.LookupTXT(ctx, name)
		if e == nil {
			for _, v := range values {
				if strings.Trim(v, "\"") == value {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("DNS TXT propagation timed out")
		case <-tick.C:
		}
	}
}
