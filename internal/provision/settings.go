package provision

import (
	"fmt"
	"net/url"
	"regexp"
)

type Settings struct {
	Domain          string `json:"domain"`
	ZoneID          string `json:"zone_id"`
	CloudflareToken string `json:"cloudflare_token,omitempty"`
	Email           string `json:"email"`
	AgentURL        string `json:"agent_url"`
	PublicURL       string `json:"public_url"`
	RealityTarget   string `json:"reality_target"`
	RealitySNI      string `json:"reality_sni"`
	ACMEURL         string `json:"acme_url"`
}

var domainRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func (s Settings) Validate() error {
	if !domainRE.MatchString(s.Domain) || s.ZoneID == "" || s.CloudflareToken == "" || s.Email == "" {
		return fmt.Errorf("domain, zone ID, Cloudflare token and ACME email are required")
	}
	for _, v := range []string{s.AgentURL, s.PublicURL} {
		u, e := url.Parse(v)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Path != "" {
			return fmt.Errorf("panel URLs must be HTTPS origins without a path")
		}
	}
	if s.RealitySNI == "" || s.RealityTarget == "" {
		return fmt.Errorf("REALITY target and SNI required")
	}
	return nil
}
