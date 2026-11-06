package provision

import (
	"github.com/rahpanel/rahpanel/internal/core"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreflightProtectsOtherServicesOnSharedAndManagedHosts(t *testing.T) {
	bash := os.Getenv("TEST_BASH")
	if bash == "" {
		var err error
		bash, err = exec.LookPath("bash")
		if err != nil {
			t.Skip("requires bash")
		}
	}
	for _, tc := range []struct{ name, managed, listeners, want string }{
		{"nginx443isnotclaimed", "", "443=999", ""},
		{"foreignselectedport", "", "8443=999", "PORT_CONFLICT:8443"},
		{"ownreinstall", "node-test", "2087=321;8443=321", ""},
		{"foreignportonmanagedhost", "node-test", "8443=999", "PORT_CONFLICT:8443"},
		{"mixedowners", "node-test", "2087=321;2087=999", "PORT_CONFLICT:2087"},
		{"differentnodeinstallation", "other-node", "", "MANAGED_NODE_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			managed := filepath.Join(dir, "node")
			os.MkdirAll(bin, 0700)
			os.MkdirAll(managed, 0700)
			if tc.managed != "" {
				os.WriteFile(filepath.Join(managed, "managed"), []byte(tc.managed+"\n"), 0600)
			}
			mocks := map[string]string{
				"id":        "echo 0\n",
				"df":        "printf 'Filesystem MB Used Available Use Mount\\n/dev/fake 4096 1 4095 1 /\\n'\n",
				"systemctl": "echo 321\n",
				"ss": `for entry in ${FAKE_LISTENERS//;/ }; do
 port=${entry%%=*}; pid=${entry#*=}
 if [[ "$*" == *":$port" ]]; then printf 'LISTEN 0 511 0.0.0.0:%s 0.0.0.0:* users:(("xray",pid=%s,fd=3))\n' "$port" "$pid"; fi
done
exit 0
`,
			}
			for name, body := range mocks {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env bash\n"+body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			script, err := preflightScript(core.Node{ID: "node-test", SharedHost: true})
			if err != nil {
				t.Fatal(err)
			}
			shellPath := func(p string) string {
				p = filepath.ToSlash(p)
				if runtime.GOOS == "windows" {
					p = "/" + strings.ToLower(p[:1]) + p[2:]
				}
				return p
			}
			script = strings.Replace(script, ". /etc/os-release", "ID=ubuntu\nVERSION_ID=24.04", 1)
			script = strings.Replace(script, "managed_dir='/etc/rahpanel'", "managed_dir="+shellQuote(shellPath(managed)), 1)
			script = "export PATH=" + shellQuote(shellPath(bin)) + ":$PATH\n" + script
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = append(os.Environ(), "FAKE_LISTENERS="+tc.listeners)
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v %s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("missing protected conflict %s: %v %s", tc.want, err, out)
			}
		})
	}
}

func TestNodeInstallerOpensOnlySelectedPublicPorts(t *testing.T) {
	n := core.Node{ID: "node-test", SharedHost: true}
	script, err := installNodeScript("/tmp/rah-stage", n)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"ufw allow 2087/tcp", "ufw allow 8443/tcp", "ufw allow 2053/tcp"} {
		if !strings.Contains(script, rule) {
			t.Fatal("missing selected firewall rule", rule)
		}
	}
	for _, unsafe := range []string{"ufw allow 443/", "ufw allow 10086/", "stop nginx", "stop xray.service"} {
		if strings.Contains(script, unsafe) {
			t.Fatal("unrelated listener/service touched", unsafe)
		}
	}
}
