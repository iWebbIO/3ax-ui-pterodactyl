package controller

import (
	"errors"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// MtprotoController exposes the per-client MTProto API, mirroring AwgController.
// Clients live in the dedicated mtproto_clients table (unique Uuid, NON-unique
// Email), so a single inbound may serve many same-named clients on one port.
type MtprotoController struct {
	clientService service.MtprotoClientService
}

func NewMtprotoController(g *gin.RouterGroup) *MtprotoController {
	a := &MtprotoController{}
	a.initRouter(g)
	return a
}

func (a *MtprotoController) initRouter(g *gin.RouterGroup) {
	g.GET("/clients", a.getClients)
	g.POST("/client/add", a.addClient)
	g.POST("/client/updateByUuid/:uuid", a.updateClientByUUID)
	g.POST("/client/delByUuid/:uuid", a.deleteClientByUUID)
	g.POST("/client/toggleByUuid/:uuid", a.toggleClientByUUID)
	g.POST("/client/resetTrafficByUuid/:uuid", a.resetClientTrafficByUUID)
}

func (a *MtprotoController) getClients(c *gin.Context) {
	clients, err := a.clientService.GetClients()
	if err != nil {
		jsonMsg(c, "get MTProto clients", err)
		return
	}
	jsonObj(c, clients, nil)
}

func (a *MtprotoController) addClient(c *gin.Context) {
	var client model.MtprotoClient
	if err := c.ShouldBindJSON(&client); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	if err := a.clientService.AddClient(&client); err != nil {
		jsonMsg(c, "add MTProto client", err)
		return
	}
	jsonObj(c, client, nil)
}

func (a *MtprotoController) updateClientByUUID(c *gin.Context) {
	clientUUID := c.Param("uuid")
	if clientUUID == "" {
		jsonMsg(c, "invalid uuid", errors.New("missing uuid"))
		return
	}
	var client model.MtprotoClient
	if err := c.ShouldBindJSON(&client); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	err := a.clientService.UpdateClientByUuid(clientUUID, &client)
	jsonMsg(c, "MTProto client updated", err)
}

func (a *MtprotoController) deleteClientByUUID(c *gin.Context) {
	clientUUID := c.Param("uuid")
	if clientUUID == "" {
		jsonMsg(c, "invalid uuid", errors.New("missing uuid"))
		return
	}
	err := a.clientService.DeleteClientByUuid(clientUUID)
	jsonMsg(c, "MTProto client deleted", err)
}

func (a *MtprotoController) toggleClientByUUID(c *gin.Context) {
	clientUUID := c.Param("uuid")
	if clientUUID == "" {
		jsonMsg(c, "invalid uuid", errors.New("missing uuid"))
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	err := a.clientService.ToggleClientByUuid(clientUUID, body.Enable)
	jsonMsg(c, "MTProto client toggled", err)
}

func (a *MtprotoController) resetClientTrafficByUUID(c *gin.Context) {
	clientUUID := c.Param("uuid")
	if clientUUID == "" {
		jsonMsg(c, "invalid uuid", errors.New("missing uuid"))
		return
	}
	err := a.clientService.ResetClientTrafficByUuid(clientUUID)
	jsonMsg(c, "MTProto client traffic reset", err)
}
