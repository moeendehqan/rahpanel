package provision

import (
	_ "embed"
	"fmt"
	"regexp"

	"github.com/rahpanel/rahpanel/internal/core"
)

const nginxSitePort = 8444

//go:embed nginx_reality.py
var nginxReality string

var nginxServerName = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9.-]*\.[a-z0-9]+$`)

func nginxRealityScript(n core.Node, mode string) (string, error) {
	if !nginxServerName.MatchString(n.RealitySNI) {
		return "", fmt.Errorf("Nginx TCP 443 sharing requires a DNS hostname as the REALITY SNI")
	}
	return fmt.Sprintf("python3 - %s %s %d %d <<'RAH_NGINX_PY'\n%s\nRAH_NGINX_PY\n",
		shellQuote(mode), shellQuote(n.RealitySNI), core.RealityNginxBackend, nginxSitePort, nginxReality), nil
}
