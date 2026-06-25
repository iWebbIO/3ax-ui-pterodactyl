package model

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateFakeTLSSecret(t *testing.T) {
	domain := "www.cloudflare.com"
	s := GenerateFakeTLSSecret(domain)
	if !strings.HasPrefix(s, "ee") {
		t.Fatalf("secret must start with ee, got %q", s)
	}
	wantSuffix := hex.EncodeToString([]byte(domain))
	if !strings.HasSuffix(s, wantSuffix) {
		t.Fatalf("secret must end with hex(domain) %q, got %q", wantSuffix, s)
	}
	if len(s) != 2+32+len(wantSuffix) {
		t.Fatalf("unexpected secret length %d", len(s))
	}
	if _, err := hex.DecodeString(s[2:34]); err != nil {
		t.Fatalf("middle is not valid hex: %v", err)
	}
}

func TestHealMtprotoClientSecret(t *testing.T) {
	domain := "example.com"
	suffix := hex.EncodeToString([]byte(domain))

	// An empty secret is populated with a valid FakeTLS secret for the domain.
	got := HealMtprotoClientSecret("", domain)
	if !strings.HasPrefix(got, "ee") || !strings.HasSuffix(got, suffix) {
		t.Fatalf("healed secret malformed: %q", got)
	}
	if len(got) != 2+32+len(suffix) {
		t.Fatalf("unexpected secret length %d", len(got))
	}

	// Re-healing against the same domain is stable (same value back).
	if again := HealMtprotoClientSecret(got, domain); again != got {
		t.Fatalf("re-heal against the same domain must be stable: %q vs %q", again, got)
	}

	// A domain change rewrites the suffix but preserves the random middle.
	mid := got[2:34]
	newDomain := "telegram.org"
	got2 := HealMtprotoClientSecret(got, newDomain)
	if got2[2:34] != mid {
		t.Fatalf("random middle should be preserved on domain change: %q vs %q", got2[2:34], mid)
	}
	if !strings.HasSuffix(got2, hex.EncodeToString([]byte(newDomain))) {
		t.Fatalf("suffix not updated for new domain: %q", got2)
	}

	// An empty domain leaves the secret untouched.
	if same := HealMtprotoClientSecret("ee-unchanged", ""); same != "ee-unchanged" {
		t.Fatalf("empty domain must not change the secret: %q", same)
	}
}
