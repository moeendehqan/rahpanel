// rahctl prepares a deployment without requiring Go or Node on managed nodes.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/rahpanel/rahpanel/internal/core"
	"github.com/rahpanel/rahpanel/internal/provision"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: rahctl init | artifacts | healthcheck")
	}
	switch os.Args[1] {
	case "init":
		initDeployment()
	case "artifacts":
		artifacts()
	case "healthcheck":
		healthcheck()
	default:
		fatal("unknown command")
	}
}
func fatal(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(1) }
func must(e error) {
	if e != nil {
		fatal(e.Error())
	}
}
func initDeployment() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	domain := fs.String("domain", "", "panel hostname")
	agent := fs.String("agent-host", "", "agent endpoint hostname; defaults to panel hostname")
	directory := fs.String("dir", ".", "output directory")
	fs.Parse(os.Args[2:])
	if !regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}$`).MatchString(*domain) {
		fatal("valid --domain required")
	}
	if *agent == "" {
		*agent = *domain
	}
	if !regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}$`).MatchString(*agent) {
		fatal("invalid agent hostname")
	}
	must(os.MkdirAll(*directory, 0700))
	path := filepath.Join(*directory, ".env")
	if _, e := os.Stat(path); e == nil {
		fatal(".env already exists; will not overwrite deployment secrets")
	}
	master := make([]byte, 32)
	mustRead(master)
	password := core.RandomToken()
	dbpass := core.RandomToken()
	content := fmt.Sprintf("PANEL_HOST=%s\nPUBLIC_URL=https://%s\nAGENT_URL=https://%s:9443\nPOSTGRES_PASSWORD=%s\nDATABASE_URL=postgres://rah:%s@db:5432/rah?sslmode=disable\nMASTER_KEY=%s\nADMIN_USERNAME=admin\nADMIN_PASSWORD=%s\n", *domain, *domain, *agent, dbpass, dbpass, base64.StdEncoding.EncodeToString(master), password)
	must(os.WriteFile(path, []byte(content), 0600))
	instructions := "Username: admin\nPassword: " + password + "\nKeep this file offline. Initial administrator is created only when the database is empty.\n"
	must(os.WriteFile(filepath.Join(*directory, "bootstrap-admin.txt"), []byte(instructions), 0600))
	fmt.Println("Created .env and bootstrap-admin.txt. Use the administrator username and password to log in.")
}
func mustRead(b []byte) { _, e := rand.Read(b); must(e) }
func fetch(ctx context.Context, u string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, e
	}
	res, e := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("artifact download HTTP %d", res.StatusCode)
	}
	if res.ContentLength > 100<<20 {
		return nil, fmt.Errorf("artifact too large")
	}
	return io.ReadAll(io.LimitReader(res.Body, 100<<20))
}
func artifacts() {
	fs := flag.NewFlagSet("artifacts", flag.ExitOnError)
	version := fs.String("version", "v25.3.6", "explicit Xray release tag")
	dir := fs.String("dir", "artifacts", "artifact directory")
	fs.Parse(os.Args[2:])
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(*version) {
		fatal("explicit numeric release tag required")
	}
	must(os.MkdirAll(*dir, 0700))
	lock := provision.ArtifactLock{Version: *version, SHA256: map[string]string{}}
	if b, e := os.ReadFile(filepath.Join(*dir, "xray-lock.json")); e == nil {
		must(json.Unmarshal(b, &lock))
		if lock.Version != *version {
			fatal("existing artifact lock is for another version; use a new release directory")
		}
	}
	ctx := context.Background()
	for arch, name := range map[string]string{"amd64": "Xray-linux-64.zip", "arm64": "Xray-linux-arm64-v8a.zip"} {
		base := "https://github.com/XTLS/Xray-core/releases/download/" + *version + "/" + name
		expected := lock.SHA256[arch]
		if expected == "" {
			digest, e := fetch(ctx, base+".dgst")
			must(e)
			m := regexp.MustCompile(`(?im)^SHA2-256=\s*([a-f0-9]{64})`).FindSubmatch(digest)
			if len(m) != 2 {
				fatal("official checksum missing")
			}
			expected = strings.ToLower(string(m[1]))
		}
		archive, e := fetch(ctx, base)
		must(e)
		h := sha256.Sum256(archive)
		if hex.EncodeToString(h[:]) != expected {
			fatal("official Xray checksum mismatch")
		}
		must(os.WriteFile(filepath.Join(*dir, "xray-linux-"+arch+".zip"), archive, 0600))
		lock.SHA256[arch] = expected
		fmt.Println("Verified Xray archive:", arch)
	}
	b, e := json.MarshalIndent(lock, "", "  ")
	must(e)
	must(os.WriteFile(filepath.Join(*dir, "xray-lock.json"), b, 0600))
}
