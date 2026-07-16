// Package cloudflared manages a bundled cloudflared (Cloudflare Tunnel / Argo
// Tunnel) sidecar process. A tunnel is an outbound-only connection to
// Cloudflare's edge, so it exposes the panel (or any local service) at a real
// HTTPS hostname with no inbound port, no root and no certificate management —
// exactly what an unprivileged Pterodactyl container needs.
//
// Two modes are supported:
//   - quick: `cloudflared tunnel --url <target>` → an ephemeral
//     *.trycloudflare.com URL, no Cloudflare account required. The public URL is
//     parsed from cloudflared's log and surfaced in Status.
//   - token: `cloudflared tunnel run --token <token>` → a named tunnel whose
//     ingress (hostname → service) is configured in the Cloudflare Zero Trust
//     dashboard. Persistent, uses your own domain.
package cloudflared

import (
	"fmt"
	"os"
	"strings"
)

// Mode selects how the tunnel is established.
type Mode string

const (
	ModeQuick Mode = "quick" // ephemeral trycloudflare.com URL, no account
	ModeToken Mode = "token" // named tunnel via a Zero Trust connector token
)

// Config is the resolved tunnel configuration the manager acts on.
type Config struct {
	Enabled bool
	Mode    Mode
	Token   string // connector token (token mode)
	Target  string // local service URL to expose (quick mode), e.g. http://127.0.0.1:2053
}

// Environment overrides. These let a Pterodactyl egg variable drive the tunnel
// without touching the panel UI; when set they win over the stored settings.
const (
	EnvEnable = "XUI_CF_ENABLE"
	EnvMode   = "XUI_CF_MODE"
	EnvToken  = "XUI_CF_TOKEN"
	EnvTarget = "XUI_CF_TARGET"
)

// Resolve overlays environment variables on top of the panel-stored settings and
// normalizes the result. Env wins so egg variables take effect immediately.
func Resolve(dbEnabled bool, dbMode, dbToken, dbTarget string) Config {
	cfg := Config{
		Enabled: dbEnabled,
		Mode:    Mode(strings.TrimSpace(dbMode)),
		Token:   strings.TrimSpace(dbToken),
		Target:  strings.TrimSpace(dbTarget),
	}

	if v, ok := os.LookupEnv(EnvEnable); ok {
		cfg.Enabled = isTruthy(v)
	}
	if v := strings.TrimSpace(os.Getenv(EnvMode)); v != "" {
		cfg.Mode = Mode(v)
	}
	if v, ok := os.LookupEnv(EnvToken); ok {
		cfg.Token = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(EnvTarget); ok {
		cfg.Target = strings.TrimSpace(v)
	}

	// Convenience: a token supplied purely via env implies an enabled named
	// tunnel unless the operator explicitly disabled it.
	if _, tokenFromEnv := os.LookupEnv(EnvToken); tokenFromEnv && cfg.Token != "" {
		if _, enableSet := os.LookupEnv(EnvEnable); !enableSet {
			cfg.Enabled = true
		}
		cfg.Mode = ModeToken
	}

	// Infer a mode when none was given: token if we have one, else quick.
	if cfg.Mode != ModeQuick && cfg.Mode != ModeToken {
		if cfg.Token != "" {
			cfg.Mode = ModeToken
		} else {
			cfg.Mode = ModeQuick
		}
	}
	return cfg
}

// Valid reports whether an enabled config has everything it needs to start.
func (c Config) Valid() error {
	if !c.Enabled {
		return nil
	}
	switch c.Mode {
	case ModeToken:
		if c.Token == "" {
			return fmt.Errorf("cloudflared: token mode requires a connector token")
		}
	case ModeQuick:
		if strings.TrimSpace(c.Target) == "" {
			return fmt.Errorf("cloudflared: quick mode requires a target URL")
		}
	default:
		return fmt.Errorf("cloudflared: unknown mode %q", c.Mode)
	}
	return nil
}

// fingerprint changes whenever a field that requires a process restart changes.
func (c Config) fingerprint() string {
	return fmt.Sprintf("%v|%s|%s|%s", c.Enabled, c.Mode, c.Token, c.Target)
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
