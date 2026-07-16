//go:build !wg_userspace

package wgengine

// This file is compiled into the DEFAULT build (no `wg_userspace` tag). It
// leaves newTunnel nil, so requesting userspace mode without the tag fails fast
// with ErrNotBuilt instead of pulling gVisor / amneziawg-go into every build.
//
// The real implementation is tunnel_userspace.go, compiled only under
// `-tags wg_userspace` (used by the Pterodactyl image).
