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
