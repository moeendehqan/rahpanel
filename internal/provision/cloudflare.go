package provision

import (
	"bytes"
	"context"
	"crypto/rand"
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

type cloudflareRejection int

func (e cloudflareRejection) Error() string {
	return fmt.Sprintf("Cloudflare rejected request (HTTP %d); verify token permissions and zone", e)
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
		return cloudflareRejection(res.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(response.Result, out)
	}
	return nil
}

// TestWrite verifies access to the zone and creates and removes one probe record.
func (d DNS) TestWrite(ctx context.Context, domain string) error {
	var zone struct {
		Name string `json:"name"`
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	err := d.call(readCtx, "GET", "", nil, &zone)
	cancelRead()
	if err != nil || zone.Name == "" {
		return fmt.Errorf("خواندن Zone در Cloudflare ناموفق بود. شناسهٔ Zone، دسترسی توکن و اتصال سرور را بررسی کنید")
	}
	if domain != zone.Name && !strings.HasSuffix(domain, "."+zone.Name) {
		return fmt.Errorf("دامنهٔ پایه در Zone انتخاب‌شده قرار ندارد")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("تست Cloudflare لغو شد")
	}

	name := "_rahpanel-test-" + strings.ToLower(rand.Text()) + "." + domain
	probe := record{Type: "TXT", Name: name, Content: "rahpanel-test", Comment: "rahpanel:cloudflare-test", TTL: 60}
	var created record
	// Once the write starts, allow it and its cleanup to finish after a disconnect.
	detached := context.WithoutCancel(ctx)
	createCtx, cancelCreate := context.WithTimeout(detached, 5*time.Second)
	err = d.call(createCtx, "POST", "/dns_records", probe, &created)
	cancelCreate()
	if rejection, ok := err.(cloudflareRejection); ok && rejection >= 400 && rejection < 500 && rejection != http.StatusRequestTimeout {
		return fmt.Errorf("Cloudflare ساخت رکورد TXT را رد کرد. مجوز DNS Edit را بررسی کنید")
	}
	if err != nil || created.ID == "" {
		return fmt.Errorf("ساخت رکورد TXT تأیید نشد. وجود رکورد %s را در Cloudflare بررسی کنید", name)
	}

	deleteCtx, cancelDelete := context.WithTimeout(detached, 5*time.Second)
	defer cancelDelete()
	if err := d.Delete(deleteCtx, created.ID); err != nil {
		return fmt.Errorf("حذف رکورد TXT تأیید نشد. رکورد %s را در Cloudflare بررسی کنید", name)
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
