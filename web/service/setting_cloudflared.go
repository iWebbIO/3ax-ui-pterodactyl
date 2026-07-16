package service

import (
	"fmt"

	"github.com/coinman-dev/3ax-ui/v2/cloudflared"
)

// Cloudflare Tunnel settings. Values are stored in the settings table and may be
// overridden at runtime by XUI_CF_* environment variables (see cloudflared.Resolve).

func (s *SettingService) GetCfTunnelEnable() (bool, error) {
	return s.getBool("cfTunnelEnable")
}

func (s *SettingService) SetCfTunnelEnable(v bool) error {
	return s.setBool("cfTunnelEnable", v)
}

func (s *SettingService) GetCfTunnelMode() (string, error) {
	return s.getString("cfTunnelMode")
}

func (s *SettingService) SetCfTunnelMode(v string) error {
	return s.setString("cfTunnelMode", v)
}

func (s *SettingService) GetCfTunnelToken() (string, error) {
	return s.getString("cfTunnelToken")
}

func (s *SettingService) SetCfTunnelToken(v string) error {
	return s.setString("cfTunnelToken", v)
}

func (s *SettingService) GetCfTunnelTarget() (string, error) {
	return s.getString("cfTunnelTarget")
}

func (s *SettingService) SetCfTunnelTarget(v string) error {
	return s.setString("cfTunnelTarget", v)
}

// GetCloudflaredConfig builds the effective tunnel config from stored settings,
// defaulting the quick-mode target to the local panel, then overlaying any
// XUI_CF_* environment overrides. This is what the panel lifecycle and the
// cloudflared controller hand to cloudflared.GetManager().Apply.
func (s *SettingService) GetCloudflaredConfig() cloudflared.Config {
	enabled, _ := s.GetCfTunnelEnable()
	mode, _ := s.GetCfTunnelMode()
	token, _ := s.GetCfTunnelToken()
	target, _ := s.GetCfTunnelTarget()

	if target == "" {
		port, err := s.GetPort()
		if err == nil && port > 0 {
			target = fmt.Sprintf("http://127.0.0.1:%d", port)
		}
	}
	return cloudflared.Resolve(enabled, mode, token, target)
}
