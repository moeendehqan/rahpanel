package provision

import "testing"

func TestUseRealityDefaults(t *testing.T) {
	for _, tc := range []struct{ target, sni, wantTarget, wantSNI string }{
		{"", "", "www.cloudflare.com:443", "www.cloudflare.com"},
		{"www.microsoft.com:443", "www.microsoft.com", "www.cloudflare.com:443", "www.cloudflare.com"},
		{"custom.example:443", "custom.example", "custom.example:443", "custom.example"},
	} {
		settings := Settings{RealityTarget: tc.target, RealitySNI: tc.sni}
		settings.UseRealityDefaults()
		if settings.RealityTarget != tc.wantTarget || settings.RealitySNI != tc.wantSNI {
			t.Errorf("defaults for %q / %q = %q / %q", tc.target, tc.sni, settings.RealityTarget, settings.RealitySNI)
		}
	}
}
