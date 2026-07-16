//go:build !linux

package cloudflared

import "os/exec"

// attachChildLifetime is a no-op off Linux (Pdeathsig is Linux-only).
func attachChildLifetime(_ *exec.Cmd) {}
