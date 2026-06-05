package server

import (
	"fmt"
	"os/exec"
	"strings"
)

// FirewallPayload is the request body sent by the panel when whitelist settings change.
type FirewallPayload struct {
	Port     int              `json:"port"`
	Protocol string           `json:"protocol"` // "tcp", "udp", "both"
	Enabled  bool             `json:"enabled"`
	Rules    []FirewallIPRule `json:"rules"`
}

// FirewallIPRule is a single entry in the allow/deny list.
type FirewallIPRule struct {
	IP     string `json:"ip"`
	Action string `json:"action"` // "allow" or "deny"
}

// SyncFirewall applies or removes iptables whitelist rules for a single allocation port.
// It is idempotent: existing rules for the port are always cleared before new ones are applied.
func (s *Server) SyncFirewall(p FirewallPayload) error {
	for _, proto := range resolveProtocols(p.Protocol) {
		if err := syncProto(proto, p); err != nil {
			return err
		}
	}
	return nil
}

func syncProto(proto string, p FirewallPayload) error {
	chain := firewallChain(proto, p.Port)

	// Always tear down existing rules first so the operation is idempotent.
	_ = iptables("-D", "INPUT", "-p", proto, "--dport", fmt.Sprintf("%d", p.Port), "-j", chain)
	_ = iptables("-F", chain)
	_ = iptables("-X", chain)

	if !p.Enabled {
		return nil
	}

	// Create a fresh chain for this port.
	if err := iptables("-N", chain); err != nil {
		return fmt.Errorf("firewall: create chain %s: %w", chain, err)
	}

	// Add an ACCEPT rule for every explicitly allowed IP/CIDR.
	for _, rule := range p.Rules {
		if rule.Action != "allow" {
			continue
		}
		if err := iptables("-A", chain, "-s", rule.IP, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("firewall: accept %s on %s/%d: %w", rule.IP, proto, p.Port, err)
		}
	}

	// Implicit DROP for everything not matched above.
	if err := iptables("-A", chain, "-j", "DROP"); err != nil {
		return fmt.Errorf("firewall: drop rule on %s/%d: %w", proto, p.Port, err)
	}

	// Insert the jump at the top of INPUT so it takes priority.
	if err := iptables("-I", "INPUT", "-p", proto, "--dport", fmt.Sprintf("%d", p.Port), "-j", chain); err != nil {
		return fmt.Errorf("firewall: jump rule on %s/%d: %w", proto, p.Port, err)
	}

	return nil
}

// firewallChain returns the iptables chain name for a given protocol and port.
// Chain names are limited to 29 characters; "WINGS-FW-TCP-65535" is 18 chars max.
func firewallChain(proto string, port int) string {
	return fmt.Sprintf("WINGS-FW-%s-%d", strings.ToUpper(proto), port)
}

func resolveProtocols(protocol string) []string {
	if protocol == "both" {
		return []string{"tcp", "udp"}
	}
	if protocol == "udp" {
		return []string{"udp"}
	}
	return []string{"tcp"}
}

func iptables(args ...string) error {
	return exec.Command("iptables", args...).Run()
}
