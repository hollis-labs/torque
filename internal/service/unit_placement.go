package service

import (
	"fmt"
	"sort"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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

// prepareFormation requires u.mu. All placement inputs become immutable once
// the complete request is saved in the attempt record.
func (u *UnitService) prepareFormation(taskID string, plan unitplan.Plan, run *UnitRun) error {
	if int(plan.Workers) > 1 && len(u.cfg.WorkerPool) < int(plan.Workers) {
		return &ValidationError{Field: "workers", Message: fmt.Sprintf("%d workers need %d pre-registered pool identities; TORQUE_UNIT_WORKER_POOL lists %d (use workers=1 or register identities)", plan.Workers, plan.Workers, len(u.cfg.WorkerPool))}
	}
	placement, pin, err := u.selectPlacement(taskID, plan)
	if err != nil {
		return err
	}
	run.Placement = placement
	form := u.buildForm(taskID, run.Attempt, plan, plan.TimeoutDuration(u.cfg.DefaultTimeout))
	if pin != nil {
		slots := form.Team["slots"].([]tetherclient.TeamSlot)
		for i := range slots {
			if slots[i].Name == "workers" {
				slots[i].DefinitionRef = *pin
			}
		}
	}
	run.Formation = &form
	return nil
}

// reserveFormation runs after settling a previous obligation. Choose the host
// and save its reservation under the admission lock, before any formation call.
// Doing this after settlement prevents an unsaved choice from racing another
// task that is admitted while the old run is being dissolved.
func (u *UnitService) reserveFormation(taskID string, plan unitplan.Plan, run *UnitRun) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.prepareFormation(taskID, plan, run); err != nil {
		return err
	}
	if err := u.saveRun(taskID, *run, sqlstore.TaskUpdate{}); err != nil {
		return fmt.Errorf("unit dispatch: record attempt: %w", err)
	}
	return nil
}
