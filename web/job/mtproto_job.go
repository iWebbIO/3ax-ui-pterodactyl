package job

import (
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/mtproto"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// MtprotoJob reconciles the running mtg sidecar processes against the enabled
// mtproto inbounds in the database, restarts any that crashed, and folds the
// per-inbound traffic scraped from each mtg metrics endpoint into the usual
// inbound traffic accounting.
type MtprotoJob struct {
	inboundService service.InboundService
}

// NewMtprotoJob creates a new mtproto reconcile/traffic job instance.
func NewMtprotoJob() *MtprotoJob {
	return new(MtprotoJob)
}

// Run reconciles desired mtproto inbounds with running mtg processes and
// records traffic deltas.
func (j *MtprotoJob) Run() {
	inbounds, err := j.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("mtproto job: get inbounds failed:", err)
		return
	}

	var desired []mtproto.Instance
	routedTags := make(map[string]bool)
	for _, ib := range inbounds {
		if ib.Protocol != model.MTProto || !ib.Enable {
			continue
		}
		if inst, ok := mtproto.InstanceFromInbound(ib); ok {
			desired = append(desired, inst)
			if inst.RouteThroughXray {
				routedTags[inst.Tag] = true
			}
		}
	}

	mgr := mtproto.GetManager()
	mgr.Reconcile(desired)

	deltas := mgr.CollectTraffic()
	if len(deltas) == 0 {
		return
	}
	// Fold each scraped delta into both the per-client traffic (by email) and an
	// aggregate per-inbound counter (by tag), mirroring how client-bearing
	// protocols are metered.
	clientTraffics := make([]*xray.ClientTraffic, 0, len(deltas))
	inboundAgg := make(map[string]*xray.Traffic)
	for _, d := range deltas {
		// Per-client deltas come from the proxy's /stats and are valid regardless
		// of egress mode, so always meter them (per-client quotas/usage).
		if d.Email != "" {
			clientTraffics = append(clientTraffics, &xray.ClientTraffic{
				Email: d.Email,
				Up:    d.Up,
				Down:  d.Down,
			})
		}
		// The inbound aggregate, however, is double-counted for routed inbounds:
		// their egress goes through the Xray SOCKS bridge (tagged with the
		// inbound's tag) which xray_traffic_job already meters. Skip the aggregate
		// for those; keep it for direct inbounds.
		if routedTags[d.Tag] {
			continue
		}
		agg := inboundAgg[d.Tag]
		if agg == nil {
			agg = &xray.Traffic{IsInbound: true, Tag: d.Tag}
			inboundAgg[d.Tag] = agg
		}
		agg.Up += d.Up
		agg.Down += d.Down
	}
	if len(clientTraffics) == 0 && len(inboundAgg) == 0 {
		return
	}
	traffics := make([]*xray.Traffic, 0, len(inboundAgg))
	for _, t := range inboundAgg {
		traffics = append(traffics, t)
	}
	if _, _, err := j.inboundService.AddTraffic(traffics, clientTraffics); err != nil {
		logger.Warning("mtproto job: add traffic failed:", err)
	}
}
