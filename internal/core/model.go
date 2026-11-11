package core

import "time"

type User struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	ExpiresAt  time.Time `json:"expires_at"`
	QuotaBytes int64     `json:"quota_bytes"`
	UsedBytes  int64     `json:"used_bytes"`
	GroupID    string    `json:"group_id"`
	Token      string    `json:"token,omitempty"`
}
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type RouteQuality struct {
	Published bool      `json:"-"`
	Weak      bool      `json:"weak"`
	LatencyMS int64     `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
}

type Node struct {
	RouteQuality   map[string]RouteQuality `json:"route_quality,omitempty"`
	SharedHost     bool                    `json:"shared_host"`
	Ports          NodePorts               `json:"ports"`
	PortsLocked    bool                    `json:"ports_locked"`
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	Host           string                  `json:"host"`
	Domain         string                  `json:"domain"`
	GroupID        string                  `json:"group_id"`
	Enabled        bool                    `json:"enabled"`
	Ready          bool                    `json:"ready"`
	Verified       []string                `json:"verified"`
	LastSeen       time.Time               `json:"last_seen"`
	AppliedVersion string                  `json:"applied_version"`
	LastError      string                  `json:"last_error"`
	RealityTarget  string                  `json:"reality_target"`
	RealitySNI     string                  `json:"reality_sni"`
	RealityPrivate string                  `json:"-"`
	RealityPublic  string                  `json:"reality_public"`
	ShortID        string                  `json:"short_id"`
	WSPath         string                  `json:"ws_path"`

	RealityBehindNginx bool `json:"-"`
}
type Credential struct {
	UserID   string `json:"user_id"`
	NodeID   string `json:"node_id"`
	UUID     string `json:"uuid"`
	Password string `json:"password"`
}
type Usage struct {
	UserID   string `json:"user_id"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}
type Report struct {
	ID             string  `json:"id"`
	RunID          string  `json:"run_id"`
	Sequence       int64   `json:"sequence"`
	Usage          []Usage `json:"usage"`
	AppliedVersion string  `json:"applied_version"`
	Error          string  `json:"error"`
}
type Snapshot struct {
	ClientCertificate []byte    `json:"client_certificate,omitempty"`
	ClientPrivateKey  []byte    `json:"client_private_key,omitempty"`
	ServerTime        time.Time `json:"server_time"`
	Version           string    `json:"version"`
	LeaseUntil        time.Time `json:"lease_until"`
	Config            []byte    `json:"config"`
	Users             []User    `json:"users"`
	Certificate       []byte    `json:"certificate"`
	PrivateKey        []byte    `json:"private_key"`
}
type Job struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Status    string    `json:"status"`
	Step      string    `json:"step"`
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (u User) Active(now time.Time) bool {
	return u.Enabled && now.Before(u.ExpiresAt) && (u.QuotaBytes == 0 || u.UsedBytes < u.QuotaBytes)
}
func LeaseValid(until, now time.Time) bool { return !until.IsZero() && now.Before(until) }
