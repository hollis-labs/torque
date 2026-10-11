package main

import (
	"context"
	"log"
	"time"

	"github.com/hollis-labs/torque/internal/service"
)

// sweepWorkstreamClaims checks for expired claim leases every interval. It
// logs each expired claim once, and each requeue when it happens, so an expired
// lease nobody can verify shows up in the log without repeating every minute.
func sweepWorkstreamClaims(ctx context.Context, ws *service.WorkstreamService, interval time.Duration) {
	reported := map[string]string{}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		expired, err := ws.SweepExpiredClaims(ctx)
		if err != nil {
			log.Printf("[workstream] lease sweep: %v", err)
			continue
		}
		for _, e := range expired {
			if reported[e.ClaimID] == e.Action {
				continue
			}
			reported[e.ClaimID] = e.Action
			log.Printf("[workstream] claim %s (task %s, run %s) lease expired %s: %s %s",
				e.ClaimID, e.TaskID, e.TeamRunID, e.LeaseExpiresAt.Format(time.RFC3339), e.Action, e.Detail)
		}
	}
}
