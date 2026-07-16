package controller

import (
	"github.com/coinman-dev/3ax-ui/v2/cloudflared"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// cfTunnelForm is the update payload for the Cloudflare Tunnel settings.
type cfTunnelForm struct {
	Enable bool   `json:"enable" form:"enable"`
	Mode   string `json:"mode" form:"mode"`     // quick | token
	Token  string `json:"token" form:"token"`   // connector token (token mode)
	Target string `json:"target" form:"target"` // local URL to expose (quick mode)
}

// CloudflaredController manages the bundled Cloudflare Tunnel (cloudflared).
type CloudflaredController struct {
	settingService service.SettingService
}

// NewCloudflaredController creates the controller and registers its routes.
func NewCloudflaredController(g *gin.RouterGroup) *CloudflaredController {
	a := &CloudflaredController{}
	a.initRouter(g)
	return a
}

func (a *CloudflaredController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/cloudflared")

	g.GET("/status", a.status)
	g.POST("/update", a.update)
	g.POST("/restart", a.restart)
}

// cfStatusResponse merges the live tunnel state with the stored form settings so
// the panel's Cloudflare tab can render status and populate its inputs in one call.
type cfStatusResponse struct {
	// Runtime
	Running   bool   `json:"running"`
	Installed bool   `json:"installed"`
	PublicURL string `json:"publicUrl"`
	Version   string `json:"version"`
	LastLog   string `json:"lastLog"`
	// Stored settings (form values)
	Enable bool   `json:"enable"`
	Mode   string `json:"mode"`
	Token  string `json:"token"`
	Target string `json:"target"`
	// True when XUI_CF_* env vars override the stored settings.
	EnvManaged bool `json:"envManaged"`
}

// status returns the live tunnel state plus the stored settings for the UI form.
func (a *CloudflaredController) status(c *gin.Context) {
	st := cloudflared.GetManager().Status()
	enable, _ := a.settingService.GetCfTunnelEnable()
	mode, _ := a.settingService.GetCfTunnelMode()
	token, _ := a.settingService.GetCfTunnelToken()
	target, _ := a.settingService.GetCfTunnelTarget()
	if mode == "" {
		mode = "quick"
	}
	jsonObj(c, cfStatusResponse{
		Running:    st.Running,
		Installed:  st.Installed,
		PublicURL:  st.PublicURL,
		Version:    st.Version,
		LastLog:    st.LastLog,
		Enable:     enable,
		Mode:       mode,
		Token:      token,
		Target:     target,
		EnvManaged: cloudflared.EnvManaged(),
	}, nil)
}

// update persists the tunnel settings and reconciles the running process.
func (a *CloudflaredController) update(c *gin.Context) {
	var form cfTunnelForm
	if err := c.ShouldBind(&form); err != nil {
		jsonMsg(c, "Invalid Cloudflare Tunnel settings", err)
		return
	}

	if err := a.settingService.SetCfTunnelEnable(form.Enable); err != nil {
		jsonMsg(c, "Failed to save Cloudflare Tunnel settings", err)
		return
	}
	if form.Mode != "" {
		if err := a.settingService.SetCfTunnelMode(form.Mode); err != nil {
			jsonMsg(c, "Failed to save Cloudflare Tunnel settings", err)
			return
		}
	}
	if err := a.settingService.SetCfTunnelToken(form.Token); err != nil {
		jsonMsg(c, "Failed to save Cloudflare Tunnel settings", err)
		return
	}
	if err := a.settingService.SetCfTunnelTarget(form.Target); err != nil {
		jsonMsg(c, "Failed to save Cloudflare Tunnel settings", err)
		return
	}

	if err := cloudflared.GetManager().Apply(a.settingService.GetCloudflaredConfig()); err != nil {
		jsonMsg(c, "Cloudflare Tunnel could not start", err)
		return
	}
	jsonObj(c, cloudflared.GetManager().Status(), nil)
}

// restart forces the tunnel process to relaunch with the current settings.
func (a *CloudflaredController) restart(c *gin.Context) {
	mgr := cloudflared.GetManager()
	mgr.Stop()
	if err := mgr.Apply(a.settingService.GetCloudflaredConfig()); err != nil {
		jsonMsg(c, "Cloudflare Tunnel could not restart", err)
		return
	}
	jsonObj(c, mgr.Status(), nil)
}
