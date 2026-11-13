package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/rahpanel/rahpanel/internal/provision"
	"github.com/rahpanel/rahpanel/internal/security"
	"github.com/rahpanel/rahpanel/internal/server"
	"github.com/rahpanel/rahpanel/internal/store"
	"github.com/rahpanel/rahpanel/internal/telegram"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	key, e := base64.StdEncoding.DecodeString(os.Getenv("MASTER_KEY"))
	if e != nil || len(key) != 32 {
		log.Fatal("MASTER_KEY must be base64-encoded 32 random bytes")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL required")
	}
	setup, c := context.WithTimeout(ctx, 30*time.Second)
	s, e := store.New(setup, dsn, key)
	c()
	if e != nil {
		log.Fatal("Database initialization failed: ", e)
	}
	defer s.DB.Close()
	if e = server.BootstrapAdmin(ctx, s, os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD")); e != nil {
		log.Fatal(e)
	}
	var ca security.Authority
	e = s.GetSetting(ctx, "node-ca", &ca)
	if errors.Is(e, sql.ErrNoRows) {
		created, ce := security.NewAuthority()
		if ce != nil {
			log.Fatal(ce)
		}
		ca = *created
		e = s.SetSetting(ctx, "node-ca", ca)
	}
	if e != nil {
		log.Fatal("Cannot load node CA")
	}
	public := env("PUBLIC_URL", "https://localhost")
	if !server.ValidateOrigin(public) {
		log.Fatal("Invalid PUBLIC_URL")
	}
	agentURL := env("AGENT_URL", "https://localhost:9443")
	u, e := url.Parse(agentURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" {
		log.Fatal("Invalid AGENT_URL")
	}
	pair, e := endpointCertificate(ctx, s, &ca, u.Hostname())
	if e != nil {
		log.Fatal("Cannot initialize agent endpoint identity")
	}
	var endpointIdentity atomic.Pointer[tls.Certificate]
	endpointIdentity.Store(pair)
	refresh := func() error {
		next, e := endpointCertificate(ctx, s, &ca, u.Hostname())
		if e == nil {
			endpointIdentity.Store(next)
		}
		return e
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.Certificate)
	w := provision.NewWorker(s, &ca)
	api := server.New(s, w, public, env("WEB_DIR", "web/dist"), env("INSECURE_DEV", "false") != "true")
	api.AgentURL = agentURL
	go w.Run(ctx)
	go s.MaintainUsage(ctx)
	go api.RunWebhooks(ctx)
	go w.MonitorRoutes(ctx, public)
	go maintain(ctx, s, w, refresh)
	go telegram.WatchSettings(ctx, s, public, os.Getenv("TELEGRAM_BOT_TEST") == "true", telegram.Settings{
		Token: os.Getenv("TELEGRAM_BOT_TOKEN"), Terms: os.Getenv("TELEGRAM_BOT_TERMS"), Support: os.Getenv("TELEGRAM_BOT_SUPPORT"),
	})
	admin := &http.Server{Addr: env("HTTP_ADDR", "127.0.0.1:8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	nodes := &http.Server{Addr: env("AGENT_ADDR", ":9443"), Handler: api.AgentHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return endpointIdentity.Load(), nil }, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}}
	go func() {
		log.Print("Panel HTTP listener ready")
		if e := admin.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Print(e)
			cancel()
		}
	}()
	go func() {
		if e := nodes.ListenAndServeTLS("", ""); e != nil && e != http.ErrServerClosed {
			log.Print(e)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = admin.Shutdown(shutdown)
	_ = nodes.Shutdown(shutdown)
}
func maintain(ctx context.Context, s *store.Store, w *provision.Worker, refresh func() error) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := refresh(); e != nil {
				log.Print("Agent endpoint identity renewal failed")
			}
			_, _ = s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<now()")
			var cfg provision.Settings
			if s.GetSetting(ctx, "provider", &cfg) != nil {
				continue
			}
			rows, e := s.DB.QueryContext(ctx, "SELECT id,domain FROM nodes WHERE enabled AND tls_expiry<now()+interval '30 days' AND tls_expiry IS NOT NULL")
			if e != nil {
				continue
			}
			list := [][2]string{}
			for rows.Next() {
				var id, domain string
				if rows.Scan(&id, &domain) == nil {
					list = append(list, [2]string{id, domain})
				}
			}
			rows.Close()
			for _, n := range list {
				c, cancel := context.WithTimeout(ctx, 10*time.Minute)
				_, e = w.Certificate(c, n[0], cfg, n[1])
				cancel()
				if e != nil {
					log.Print("Node certificate renewal failed for ", n[0])
				}
			}
		}
	}
}
