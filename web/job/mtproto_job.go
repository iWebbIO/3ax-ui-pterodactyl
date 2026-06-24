package job

import (
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/mtproto"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// MtprotoJob reconciles the running mtg / mtg-multi sidecars against the enabled
// mtproto inbounds and their clients (stored in the dedicated mtproto_clients
// table), restarts any that crashed, scrapes each sidecar's per-client traffic,
// and folds it into that table — then re-reconciles when quota/expiry enforcement
// changed the active client set.
type MtprotoJob struct {
	inboundService service.InboundService
	clientService  service.MtprotoClientService
}

// NewMtprotoJob creates a new mtproto reconcile/traffic job instance.
func NewMtprotoJob() *MtprotoJob {
	return new(MtprotoJob)
}

// buildDesired returns the desired sidecar instances from the current DB state:
// every enabled mtproto inbound with at least one active client.
func (j *MtprotoJob) buildDesired() ([]mtproto.Instance, map[string]bool) {
	inbounds, err := j.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("mtproto job: get inbounds failed:", err)
		return nil, nil
	}
	var desired []mtproto.Instance
	routedTags := make(map[string]bool)
	for _, ib := range inbounds {
		if ib.Protocol != model.MTProto || !ib.Enable {
			continue
		}
		clients, err := j.clientService.GetClientsByInbound(ib.Id)
		if err != nil {
			logger.Warning("mtproto job: get clients failed:", err)
			continue
		}
		if inst, ok := mtproto.InstanceFromInbound(ib, clients); ok {
			desired = append(desired, inst)
			if inst.RouteThroughXray {
				routedTags[inst.Tag] = true
			}
		}
	}
	return desired, routedTags
}

// Run reconciles desired mtproto inbounds with running sidecars, records traffic
// into the mtproto_clients table, and re-reconciles after enforcement.
func (j *MtprotoJob) Run() {
	desired, _ := j.buildDesired()

	mgr := mtproto.GetManager()
	mgr.Reconcile(desired)

	deltas := mgr.CollectTraffic()
	changed := j.clientService.RecordTraffic(deltas)
	if changed {
		// Enforcement disabled/renewed a client — rebuild the desired set so the
		// sidecar drops or re-adds the affected secret within this tick.
		desired, _ = j.buildDesired()
		mgr.Reconcile(desired)
	}
}
