package core

import "fmt"

type NodePorts struct {
	Reality   int `json:"reality"`
	WebSocket int `json:"websocket"`
	Trojan    int `json:"trojan"`
	Stats     int `json:"stats"`
}

func (p NodePorts) WithDefaults(shared bool) NodePorts {
	if p.Reality == 0 {
		p.Reality = 443
		if shared {
			p.Reality = 2087
		}
	}
	if p.WebSocket == 0 {
		p.WebSocket = 8443
	}
	if p.Trojan == 0 {
		p.Trojan = 2053
	}
	if p.Stats == 0 {
		p.Stats = 10085
		if shared {
			p.Stats = 10086
		}
	}
	return p
}
func (n Node) EffectivePorts() NodePorts { return n.Ports.WithDefaults(n.SharedHost) }
func (p NodePorts) Public() []int        { return []int{p.Reality, p.WebSocket, p.Trojan} }
func (p NodePorts) All() []int           { return []int{p.Reality, p.WebSocket, p.Trojan, p.Stats} }
func (p NodePorts) Validate(shared bool) error {
	seen := map[int]bool{}
	for _, port := range p.All() {
		if port < 1 || port > 65535 {
			return fmt.Errorf("ports must be between 1 and 65535")
		}
		if seen[port] {
			return fmt.Errorf("each connection and statistics listener needs a different port")
		}
		if shared && (port == 80 || port == 443 || port == 9443) {
			return fmt.Errorf("ports 80, 443 and 9443 are reserved for the panel on a shared host")
		}
		seen[port] = true
	}
	return nil
}
