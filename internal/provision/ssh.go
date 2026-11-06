package provision

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"github.com/pkg/sftp"
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
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
	Fingerprint string `json:"fingerprint"`
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

const preflight = `
set -euo pipefail
. /etc/os-release
[ "$ID" = ubuntu ] && [ "$VERSION_ID" = 24.04 ] || { echo OS_UNSUPPORTED; exit 1; }
[ "$(id -u)" = 0 ]
[ "$(df -Pm / | awk 'NR==2{print $4}')" -gt 512 ]
command -v systemctl >/dev/null
command -v ss >/dev/null
if [ ! -f /etc/rahpanel/managed ]; then
 for port in 443 8443 2053 10085; do
  if ss -H -ltn "sport = :$port" | grep -q .; then echo PORT_CONFLICT; exit 1; fi
 done
fi
uname -m
`
const installScript = `
set -euo pipefail
stage=STAGING_DIRECTORY
trap 'rm -rf -- "$stage"' EXIT
install -d -m 700 /etc/rahpanel
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
touch /etc/rahpanel/managed
chmod 600 /etc/rahpanel/managed
if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
 ufw allow 443/tcp comment 'rahpanel'
 ufw allow 8443/tcp comment 'rahpanel'
 ufw allow 2053/tcp comment 'rahpanel'
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
