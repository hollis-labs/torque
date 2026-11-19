package service

import (
	"fmt"
	"sort"

	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/hollis-labs/torque/internal/unitplan"
)

// selectPlacement runs under the dispatch lock. The caller persists its result
// before releasing that lock, so another dispatch sees forming reservations.
func (u *UnitService) selectPlacement(taskID string, plan unitplan.Plan) (string, *tetherclient.DefinitionRef, error) {
	hint := plan.Placement
	if hint == "catalog" || plan.WorkerRuntime != unitplan.RuntimeSol {
		return "catalog", nil, nil
	}
	placements := u.cfg.Pins.WorkerSolPlacements
	if len(placements) == 0 && (hint == "" || hint == "spread") {
		return "catalog", nil, nil // custom/legacy pin configuration
	}
	hosts := make([]string, 0, len(placements))
	for host, pin := range placements {
		// Config is operator-owned but a malformed entry must not become an
		// arbitrary or incomplete definition in a formation request.
		probe := plan
		probe.Placement = host
		if err := probe.Validate(); err != nil || host == "catalog" || host == "spread" || host == "" || pin.ID == "" || pin.Revision == "" || pin.Digest == "" {
			return "", nil, &ValidationError{Field: "placement", Message: fmt.Sprintf("invalid worker_sol_placements entry %q", host)}
		}
		hosts = append(hosts, host)
	}
	if hint != "" && hint != "spread" {
		pin, ok := placements[hint]
		if !ok {
			return "", nil, &ValidationError{Field: "placement", Message: fmt.Sprintf("Sol host %q has no configured pin", hint)}
		}
		return hint, &pin, nil
	}
	loads, err := u.store.UnitPlacementLoads(taskID)
	if err != nil {
		return "", nil, fmt.Errorf("unit dispatch: placement load: %w", err)
	}
	sort.Strings(hosts)
	selected := hosts[0]
	for _, host := range hosts[1:] {
		if loads[host] < loads[selected] {
			selected = host
		}
	}
	pin := placements[selected]
	return selected, &pin, nil
}
