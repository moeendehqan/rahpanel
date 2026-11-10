package provision

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"github.com/pkg/sftp"
	"github.com/rahpanel/rahpanel/internal/core"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type SSHCredentials struct {
	Ports       *core.NodePorts `json:"ports,omitempty"`
	Host        string          `json:"host"`
	Port        int             `json:"port"`
	Username    string          `json:"username"`
	Password    string          `json:"password,omitempty"`
	PrivateKey  string          `json:"private_key,omitempty"`
	Fingerprint string          `json:"fingerprint"`
}

func (c SSHCredentials) Address() (string, error) {
	if net.ParseIP(c.Host) == nil || c.Port < 1 || c.Port > 65535 {
		return "", fmt.Errorf("valid IP and SSH port required")
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), nil
}
func Fingerprint(ctx context.Context, host string, port int) (string, error) {
	c := SSHCredentials{Host: host, Port: port}
	address, e := c.Address()
	if e != nil {
		return "", e
	}
	conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if e != nil {
		return "", fmt.Errorf("SSH host unreachable")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	fp := ""
	config := &ssh.ClientConfig{User: "rah-fingerprint", HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
		fp = ssh.FingerprintSHA256(key)
		return fmt.Errorf("fingerprint probe complete")
	}}
	_, _, _, _ = ssh.NewClientConn(conn, address, config)
	if fp == "" {
		return "", fmt.Errorf("cannot read SSH host key")
	}
	return fp, nil
}
func connect(ctx context.Context, c SSHCredentials) (*ssh.Client, error) {
	address, e := c.Address()
	if e != nil {
		return nil, e
	}
	if c.Username == "" || c.Fingerprint == "" {
		return nil, fmt.Errorf("SSH username and confirmed fingerprint required")
	}
	auth := []ssh.AuthMethod{}
	if c.PrivateKey != "" {
		key, e := ssh.ParsePrivateKey([]byte(c.PrivateKey))
		if e != nil {
			return nil, fmt.Errorf("invalid or encrypted SSH private key")
		}
		auth = append(auth, ssh.PublicKeys(key))
	} else if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	} else {
		return nil, fmt.Errorf("SSH credentials required")
	}
	conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if e != nil {
		return nil, fmt.Errorf("SSH unreachable")
	}
	conn.SetDeadline(time.Now().Add(15 * time.Minute))
	config := &ssh.ClientConfig{User: c.Username, Auth: auth, Timeout: 15 * time.Second, HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
		if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(key)), []byte(c.Fingerprint)) != 1 {
			return fmt.Errorf("SSH host key changed")
		}
		return nil
	}}
	cc, ch, req, e := ssh.NewClientConn(conn, address, config)
	if e != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH authentication or host-key verification failed")
	}
	return ssh.NewClient(cc, ch, req), nil
}
func remote(c *ssh.Client, script string) ([]byte, error) {
	s, e := c.NewSession()
	if e != nil {
		return nil, e
	}
	defer s.Close()
	s.Stdin = strings.NewReader(script)
	if c.User() == "root" {
		return s.Output("/bin/bash -se")
	}
	return s.Output("sudo -n /bin/bash -se")
}
func upload(c *ssh.Client, dir string, files map[string][]byte) error {
	client, e := sftp.NewClient(c)
	if e != nil {
		return e
	}
	defer client.Close()
	if e = client.MkdirAll(dir); e != nil {
		return e
	}
	if e = client.Chmod(dir, 0700); e != nil {
		return e
	}
	for name, b := range files {
		f, e := client.OpenFile(dir+"/"+name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if e != nil {
			return e
		}
		if e = f.Chmod(0600); e != nil {
			f.Close()
			return e
		}
		_, e = io.Copy(f, bytes.NewReader(b))
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	return nil
}
func agentBinary(arch string) ([]byte, error) {
	return os.ReadFile(filepath.Join("artifacts", "rah-agent-linux-"+arch))
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func preflightScript(n core.Node) (string, error) {
	p := n.ListenerPorts()
	if e := p.Validate(n.SharedHost); e != nil {
		if n.RealityBehindNginx {
			return "", fmt.Errorf("Nginx TCP 443 sharing requires TCP %d for the local REALITY backend", p.Reality)
		}
		return "", e
	}
	ports := []string{}
	for _, port := range p.All() {
		ports = append(ports, strconv.Itoa(port))
	}
	nginxCandidate := !n.SharedHost && !n.PortsLocked && n.EffectivePorts().Reality == 443
	if n.RealityBehindNginx {
		for _, port := range p.All() {
			if port == nginxSitePort {
				return "", fmt.Errorf("Nginx needs TCP %d for local HTTPS", nginxSitePort)
			}
		}
		ports = append(ports, strconv.Itoa(nginxSitePort))
	}
	return "node_id=" + shellQuote(n.ID) + "\nports=" + shellQuote(strings.Join(ports, " ")) +
		"\nnginx_candidate=" + strconv.FormatBool(nginxCandidate) +
		"\nnginx_backend=" + strconv.FormatBool(n.RealityBehindNginx) +
		"\nnginx_site_port=" + strconv.Itoa(nginxSitePort) + "\n" + preflight, nil
}

const preflight = `
set -euo pipefail
managed_dir='/etc/rahpanel'
. /etc/os-release
[ "$ID" = ubuntu ] && [ "$VERSION_ID" = 24.04 ] || { echo OS_UNSUPPORTED; exit 1; }
[ "$(id -u)" = 0 ]
[ "$(df -Pm / | awk 'NR==2{print $4}')" -gt 512 ]
command -v systemctl >/dev/null
command -v ss >/dev/null
owned=0
if [ -f "$managed_dir/managed" ]; then
 owner=$(cat "$managed_dir/managed")
 if [ "$owner" = "$node_id" ] || { [ -z "$owner" ] && grep -Fq '"node_id":"'"$node_id"'"' "$managed_dir/agent.json"; }; then
  owned=1
 else
  echo MANAGED_NODE_MISMATCH; exit 1
 fi
elif [ -d "$managed_dir" ] && [ -n "$(ls -A "$managed_dir")" ]; then
 echo UNMANAGED_RAH_INSTALLATION; exit 1
fi
if [ "$owned" = 0 ] && { [ -f /etc/systemd/system/rah-agent.service ] || [ -f /etc/systemd/system/rah-xray.service ] || [ -f /usr/local/bin/rah-agent ]; }; then
 echo UNMANAGED_RAH_INSTALLATION; exit 1
fi
pid=0
if [ "$owned" = 1 ]; then pid=$(systemctl show rah-xray.service --property=MainPID --value 2>/dev/null || true); fi
for port in $ports; do
 listeners=$(ss -H -ltnp "sport = :$port")
 if [ -n "$listeners" ]; then
  if { { "$nginx_candidate" && [ "$port" = 443 ]; } || { "$nginx_backend" && [ "$port" = "$nginx_site_port" ]; }; } &&
     systemctl is-active --quiet nginx.service &&
     printf '%s\n' "$listeners" | awk '
      { line=$0; found=0;
        while(match(line,/\("[^"]+",pid=[0-9]+,fd=[0-9]+\)/)) {
         if(substr(line,RSTART,8)!="(\"nginx\"") exit 1;
         found=1; line=substr(line,RSTART+RLENGTH);
        }
        if(!found) exit 1;
      }'; then
   if [ "$port" = 443 ]; then echo NGINX_REALITY_443; fi
   continue
  fi
  if [ "$owned" != 1 ] || [ -z "$pid" ] || [ "$pid" = 0 ]; then echo "PORT_CONFLICT:$port"; exit 1; fi
  if ! printf '%s\n' "$listeners" | awk -v expected="$pid" '
   { line=$0; found=0;
     while(match(line,/pid=[0-9]+/)) {
      if(substr(line,RSTART,RLENGTH)!="pid="expected) exit 1;
      found=1; line=substr(line,RSTART+RLENGTH);
     }
     if(!found) exit 1;
   }'; then echo "PORT_CONFLICT:$port"; exit 1; fi
 fi
done
uname -m
`

func installNodeScript(stage string, n core.Node) (string, error) {
	p := n.EffectivePorts()
	if e := p.Validate(n.SharedHost); e != nil {
		return "", e
	}
	rules := []string{}
	for _, port := range p.Public() {
		rules = append(rules, fmt.Sprintf(" ufw allow %d/tcp comment 'rahpanel'", port))
	}
	return "stage=" + shellQuote(stage) + "\nnode_id=" + shellQuote(n.ID) + "\n" + strings.ReplaceAll(installScript, "FIREWALL_RULES", strings.Join(rules, "\n")), nil
}

const installScript = `
set -euo pipefail
trap 'rm -rf -- "$stage"' EXIT
install -d -m 700 /etc/rahpanel
printf '%s\n' "$node_id" > /etc/rahpanel/managed
chmod 600 /etc/rahpanel/managed
install -m 755 "$stage/agent" /usr/local/bin/rah-agent.new
install -m 755 "$stage/xray" /usr/local/bin/rah-xray.new
/usr/local/bin/rah-xray.new version >/dev/null
systemctl stop rah-agent.service 2>/dev/null || true
systemctl stop rah-watchdog.service rah-xray.service 2>/dev/null || true
mv /usr/local/bin/rah-agent.new /usr/local/bin/rah-agent
mv /usr/local/bin/rah-xray.new /usr/local/bin/rah-xray
for f in agent.json client.crt client.key ca.crt tls.crt tls.key xray.json; do
 install -m 600 "$stage/$f" "/etc/rahpanel/$f"
done
rm -f /etc/rahpanel/identity.json /etc/rahpanel/lease.json
/usr/local/bin/rah-xray run -test -config /etc/rahpanel/xray.json >/dev/null
for unit in rah-agent.service rah-watchdog.service rah-xray.service; do
 install -m 644 "$stage/$unit" "/etc/systemd/system/$unit"
done
if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
FIREWALL_RULES
fi
systemctl daemon-reload
systemctl enable rah-agent.service rah-watchdog.service
systemctl start rah-watchdog.service rah-agent.service
`

func units() map[string][]byte {
	return map[string][]byte{
		"rah-agent.service": []byte(`[Unit]
Description=Rah node reconciler
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/usr/local/bin/rah-agent
Restart=always
RestartSec=3
UMask=0077
NoNewPrivileges=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/etc/rahpanel
PrivateTmp=true
[Install]
WantedBy=multi-user.target
`),
		"rah-watchdog.service": []byte(`[Unit]
Description=Rah independent lease watchdog
After=network.target
[Service]
ExecStart=/usr/local/bin/rah-agent --watchdog
Restart=always
RestartSec=1
UMask=0077
NoNewPrivileges=true
ProtectHome=true
ProtectSystem=strict
PrivateTmp=true
[Install]
WantedBy=multi-user.target
`),
		"rah-xray.service": []byte(`[Unit]
Description=Rah managed Xray
After=network-online.target
[Service]
ExecStart=/usr/local/bin/rah-xray run -config /etc/rahpanel/xray.json
Restart=no
TimeoutStopSec=2
KillMode=control-group
UMask=0077
NoNewPrivileges=true
ProtectHome=true
ProtectSystem=strict
PrivateTmp=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
`),
	}
}
