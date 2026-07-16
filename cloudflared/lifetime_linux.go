//go:build linux

package cloudflared

import (
	"os/exec"
	"syscall"
)

// attachChildLifetime asks the kernel to send SIGTERM to cloudflared if the
// panel process dies, so a crashed/killed panel never leaves an orphaned tunnel
// holding the connection open. (Pterodactyl tears down the whole cgroup on stop,
// but this makes the guarantee explicit and helps on plain Linux hosts too.)
func attachChildLifetime(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}
