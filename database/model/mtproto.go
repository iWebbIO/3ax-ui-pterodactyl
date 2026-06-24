package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateFakeTLSSecret builds an MTProto FakeTLS secret for the given domain:
// the "ee" FakeTLS marker, 16 random bytes, then the domain encoded as hex.
// This single value is what mtg's config and the client tg:// link both use.
func GenerateFakeTLSSecret(domain string) string {
	return "ee" + mtprotoRandomMiddle() + hex.EncodeToString([]byte(domain))
}

func mtprotoRandomMiddle() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("mtproto: crypto/rand read failed: %w", err))
	}
	return hex.EncodeToString(buf)
}

// mtprotoSecretMiddle returns the 16-byte random middle of an existing secret
// when it is well-formed, otherwise a freshly generated one. Reusing the middle
// keeps the secret stable when only the FakeTLS domain changes.
func mtprotoSecretMiddle(secret string) string {
	s := secret
	if strings.HasPrefix(s, "ee") || strings.HasPrefix(s, "dd") {
		s = s[2:]
	}
	if len(s) >= 32 {
		mid := s[:32]
		if _, err := hex.DecodeString(mid); err == nil {
			return mid
		}
	}
	return mtprotoRandomMiddle()
}

// HealMtprotoSecret normalises an mtproto inbound's settings JSON before the
// value leaves for the mtg sidecar or a share link: it rebuilds `secret` so it
// is always a valid FakeTLS secret whose trailing domain matches
// `fakeTlsDomain`, generating the random middle when one is missing and
// rewriting the domain suffix when the domain changed. Returns the rewritten
// settings and true when anything changed.
func HealMtprotoSecret(settings string) (string, bool) {
	if settings == "" {
		return settings, false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return settings, false
	}
	domain, _ := parsed["fakeTlsDomain"].(string)
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return settings, false
	}
	secret, _ := parsed["secret"].(string)
	expected := "ee" + mtprotoSecretMiddle(secret) + hex.EncodeToString([]byte(domain))
	if secret == expected {
		return settings, false
	}
	parsed["secret"] = expected
	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return settings, false
	}
	return string(out), true
}

// MtprotoClient is one user of a multi-user MTProto proxy. Its Id is the stable
// key used both as the mtg-multi `[secrets]` entry name and the key under which
// per-user traffic is reported by mtg-multi's /stats endpoint. Email is the
// human label and the key for the panel's ClientStats traffic rows.
type MtprotoClient struct {
	Id         string `json:"id"`
	Secret     string `json:"secret"`
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	TotalGB    int64  `json:"totalGB"`
	LimitIp    int    `json:"limitIp"`
}

// GenerateMtprotoClientID returns a fresh client id: 8 random bytes as hex, safe
// to use as a TOML bare key in mtg-multi's [secrets] table.
func GenerateMtprotoClientID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("mtproto: crypto/rand read failed: %w", err))
	}
	return hex.EncodeToString(buf)
}

// MtprotoFakeTLSDomain extracts the inbound's fronting domain from its settings.
func MtprotoFakeTLSDomain(settings string) string {
	var parsed struct {
		FakeTlsDomain string `json:"fakeTlsDomain"`
	}
	_ = json.Unmarshal([]byte(settings), &parsed)
	return strings.TrimSpace(parsed.FakeTlsDomain)
}

// ParseMtprotoClients returns the inbound's fronting domain and its client list.
func ParseMtprotoClients(settings string) (string, []MtprotoClient) {
	var parsed struct {
		FakeTlsDomain string          `json:"fakeTlsDomain"`
		Clients       []MtprotoClient `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return "", nil
	}
	return strings.TrimSpace(parsed.FakeTlsDomain), parsed.Clients
}

// HealMtprotoClients normalises a multi-user mtproto inbound's settings: it gives
// every client a stable id, and rebuilds each client's FakeTLS secret so its
// trailing domain matches `fakeTlsDomain` (reusing the random middle so an
// unchanged client keeps its link). Returns the rewritten settings and true when
// anything changed. A legacy single-secret inbound (top-level `secret`, no
// `clients`) is left untouched here — db migration converts it first.
func HealMtprotoClients(settings string) (string, bool) {
	if settings == "" {
		return settings, false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return settings, false
	}
	domain, _ := parsed["fakeTlsDomain"].(string)
	domain = strings.TrimSpace(domain)
	clients, ok := parsed["clients"].([]any)
	if !ok || domain == "" {
		return settings, false
	}
	changed := false
	domainHex := hex.EncodeToString([]byte(domain))
	for i, c := range clients {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := cm["id"].(string); strings.TrimSpace(id) == "" {
			cm["id"] = GenerateMtprotoClientID()
			changed = true
		}
		secret, _ := cm["secret"].(string)
		expected := "ee" + mtprotoSecretMiddle(secret) + domainHex
		if secret != expected {
			cm["secret"] = expected
			changed = true
		}
		clients[i] = cm
	}
	if !changed {
		return settings, false
	}
	parsed["clients"] = clients
	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return settings, false
	}
	return string(out), true
}
