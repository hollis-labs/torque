package service_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workerPin(t *testing.T, form tetherclient.TeamForm) tetherclient.DefinitionRef {
	t.Helper()
	b, err := json.Marshal(form.Team["slots"])
	require.NoError(t, err)
	var slots []tetherclient.TeamSlot
	require.NoError(t, json.Unmarshal(b, &slots))
	for _, slot := range slots {
		if slot.Name == "workers" {
			return slot.DefinitionRef
		}
	}
	t.Fatal("no worker slot")
	return tetherclient.DefinitionRef{}
}

func TestUnitPlacementSpreadsEightUnitsAndPersistsBeforeForm(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{MaxActive: 8})
	hosts := map[string]int{}
	for i := 0; i < 8; i++ {
		id := newUnitTask(t, svc, nil)
		fake.set(func() {
			fake.onForm = func() {
				run := unitRun(t, svc, id)
				assert.Equal(t, service.UnitForming, run.State)
				assert.NotEmpty(t, run.Placement)
				assert.NotNil(t, run.Formation)
			}
		})
		_, err := dispatch(svc, id)
		require.NoError(t, err)
		run := unitRun(t, svc, id)
		hosts[run.Placement]++
		require.Equal(t, service.DefaultUnitPins().WorkerSolPlacements[run.Placement], workerPin(t, *run.Formation))
	}
	require.Len(t, hosts, 7)
	for _, n := range hosts {
		require.LessOrEqual(t, n, 2)
	}
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.ErrorIs(t, err, service.ErrUnitCapacity)
}

func TestUnitPlacementExplicitCatalogAndCustomPins(t *testing.T) {
	for _, tc := range []struct {
		name, hint, runtime, want string
		custom                    bool
	}{
		{name: "explicit", hint: "worker-4", want: "team-worker-sol-w4"},
		{name: "catalog", hint: "catalog", want: "team-worker-sol"},
		{name: "agy", runtime: "agy", want: "team-worker-agy"},
		{name: "custom", custom: true, want: "custom-sol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := service.UnitConfig{}
			if tc.custom {
				cfg.Pins = service.DefaultUnitPins()
				cfg.Pins.WorkerSolPlacements = nil
				cfg.Pins.WorkerSol.ID = "custom-sol"
			}
			svc := unitSetup(t, newFake(), cfg)
			meta := map[string]any{"placement": tc.hint}
			if tc.runtime != "" {
				meta["worker_runtime"] = tc.runtime
			}
			id := newUnitTask(t, svc, meta)
			_, err := dispatch(svc, id)
			require.NoError(t, err)
			require.Equal(t, tc.want, workerPin(t, *unitRun(t, svc, id).Formation).ID)
		})
	}
}

func TestUnitPlacementReplaySealsRequestAcrossConfigAndPlanChanges(t *testing.T) {
	fake := newFake()
	fake.loseResponse = true
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, map[string]any{"placement": "worker-3"})
	_, err := dispatch(svc, id)
	require.Error(t, err)
	before := unitRun(t, svc, id)
	require.NotNil(t, before.Formation)
	// Reconfigure while no watcher is running; the pending request must remain
	// byte-for-byte equivalent even if pins, placement and pool settings change.
	cfg := service.UnitConfig{Pins: service.DefaultUnitPins()}
	cfg.Pins.WorkerSolPlacements = nil
	cfg.Pins.WorkerSol.ID = "changed-sol"
	cfg.Pins.Orchestrator.ID = "changed-orchestrator"
	svc.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), fastCfg(cfg))
	v := unitNullStr(`{"unit_plan":{"workstream_id":"ws-7","goal":"changed","done_when":["x"],"placement":"worker-6","workers":3}}`)
	require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &v}}))
	fake.set(func() { fake.loseResponse = false })
	_, err = dispatch(svc, id)
	require.NoError(t, err)
	after := unitRun(t, svc, id)
	require.Equal(t, before.Placement, after.Placement)
	require.Equal(t, before.Deadline, after.Deadline)
	require.Equal(t, before.Formation, after.Formation)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.GreaterOrEqual(t, len(fake.formRequests), 2)
	for _, form := range fake.formRequests {
		require.Equal(t, fake.formRequests[0], form)
	}
}

func TestUnitPlacementLegacyReplayUsesOriginalBasePin(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	// A pre-upgrade forming record has no placement or formation snapshot.
	raw, err := json.Marshal(service.UnitRun{State: service.UnitForming, Attempt: 1, Key: "legacy-key", Workers: 1, WorkerRuntime: "sol"})
	require.NoError(t, err)
	v := string(raw)
	require.NoError(t, svc.Store().UpdateTask(id, sqlstore.TaskUpdate{UnitRun: &v}))
	_, err = dispatch(svc, id)
	require.NoError(t, err)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Equal(t, "team-worker-sol", workerPin(t, fake.forms[0]).ID)
	require.Empty(t, unitRun(t, svc, id).Placement)
}

func TestUnitPlacementCustomEnvDoesNotInheritFleet(t *testing.T) {
	t.Setenv("TORQUE_UNIT_PINS", `{"worker_sol":{"id":"custom","revision":"r2","digest":"d"}}`)
	cfg := service.UnitConfigFromEnv()
	require.Empty(t, cfg.Pins.WorkerSolPlacements)
	require.Equal(t, "custom", cfg.Pins.WorkerSol.ID)
	require.Equal(t, service.DefaultUnitPins().Orchestrator, cfg.Pins.Orchestrator)
	svc := unitSetup(t, newFake(), cfg)
	id := newUnitTask(t, svc, map[string]any{"placement": "worker-1"})
	_, err := dispatch(svc, id)
	require.ErrorContains(t, err, "no configured pin")
}

func TestUnitPlacementReleasedReservationAndFormingLoad(t *testing.T) {
	fake := newFake()
	fake.loseResponse = true
	pins := service.DefaultUnitPins()
	pins.WorkerSolPlacements = map[string]tetherclient.DefinitionRef{
		"worker-1": pins.WorkerSolPlacements["worker-1"],
		"worker-2": pins.WorkerSolPlacements["worker-2"],
	}
	svc := unitSetup(t, fake, service.UnitConfig{Pins: pins})
	first := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, first)
	require.Error(t, err)
	require.Equal(t, "worker-1", unitRun(t, svc, first).Placement)
	second := newUnitTask(t, svc, nil)
	_, err = dispatch(svc, second)
	require.Error(t, err)
	require.Equal(t, "worker-2", unitRun(t, svc, second).Placement)
	// A confirmed terminal record releases its placement reservation.
	run := unitRun(t, svc, first)
	run.State = service.UnitDone
	run.Unconfirmed = false
	b, err := json.Marshal(run)
	require.NoError(t, err)
	raw := string(b)
	require.NoError(t, svc.Store().UpdateTask(first, sqlstore.TaskUpdate{UnitRun: &raw}))
	third := newUnitTask(t, svc, nil)
	_, err = dispatch(svc, third)
	require.Error(t, err)
	require.Equal(t, "worker-1", unitRun(t, svc, third).Placement)
}

func TestUnitPlacementRejectsMalformedConfiguredPins(t *testing.T) {
	fake := newFake()
	pins := service.DefaultUnitPins()
	pins.WorkerSolPlacements = map[string]tetherclient.DefinitionRef{"worker-1": {ID: "missing-revision"}}
	svc := unitSetup(t, fake, service.UnitConfig{Pins: pins})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.ErrorContains(t, err, "invalid worker_sol_placements")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Zero(t, fake.formCalls)
}

func TestUnitPlacementConcurrentDispatchSeesFormingReservation(t *testing.T) {
	fake := newFake()
	fake.formDelay = 50 * time.Millisecond
	svc := unitSetup(t, fake, service.UnitConfig{})
	first := newUnitTask(t, svc, nil)
	second := newUnitTask(t, svc, nil)
	results := make(chan error, 2)
	for _, id := range []string{first, second} {
		go func(id string) { _, err := dispatch(svc, id); results <- err }(id)
	}
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.NotEqual(t, unitRun(t, svc, first).Placement, unitRun(t, svc, second).Placement)
}

func setPlacementRun(t *testing.T, svc *service.Service, id, host, state string, workers int, unconfirmed bool) {
	t.Helper()
	b, err := json.Marshal(service.UnitRun{Placement: host, State: state, Workers: workers, Unconfirmed: unconfirmed})
	require.NoError(t, err)
	raw := string(b)
	require.NoError(t, svc.Store().UpdateTask(id, sqlstore.TaskUpdate{UnitRun: &raw}))
}

func TestUnitPlacementWeightsEveryLiveObligationByWorkers(t *testing.T) {
	for _, tc := range []struct {
		state string
		flag  bool
		want  string
	}{
		{service.UnitForming, false, "worker-2"},
		{service.UnitActive, false, "worker-2"},
		{service.UnitClosing, false, "worker-2"},
		{service.UnitUnconfirmed, false, "worker-2"},
		{service.UnitFailed, true, "worker-2"},
		{service.UnitDone, false, "worker-1"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			pins := service.DefaultUnitPins()
			pins.WorkerSolPlacements = map[string]tetherclient.DefinitionRef{
				"worker-1": pins.WorkerSolPlacements["worker-1"],
				"worker-2": pins.WorkerSolPlacements["worker-2"],
			}
			svc := unitSetup(t, newFake(), service.UnitConfig{MaxActive: 8, Pins: pins, WorkerPool: []string{"msg://agent/local/pool-1", "msg://agent/local/pool-2"}})
			first := newUnitTask(t, svc, nil)
			second := newUnitTask(t, svc, nil)
			setPlacementRun(t, svc, first, "worker-1", tc.state, 3, tc.flag)
			setPlacementRun(t, svc, second, "worker-2", service.UnitActive, 2, false)
			id := newUnitTask(t, svc, map[string]any{"workers": 2})
			_, err := dispatch(svc, id)
			require.NoError(t, err)
			run := unitRun(t, svc, id)
			require.Equal(t, tc.want, run.Placement)
			require.Equal(t, 2, run.Workers)
			loads, err := svc.Store().UnitPlacementLoads("")
			require.NoError(t, err)
			if tc.state == service.UnitDone {
				require.Equal(t, 2, loads["worker-1"])
				require.Equal(t, 2, loads["worker-2"])
			}
			if tc.state != service.UnitDone {
				require.Equal(t, 3, loads["worker-1"])
				require.Equal(t, 4, loads["worker-2"])
				// The newly admitted two-worker unit changes the next choice.
				next := newUnitTask(t, svc, nil)
				_, err = dispatch(svc, next)
				require.NoError(t, err)
				require.Equal(t, "worker-1", unitRun(t, svc, next).Placement)
			}
		})
	}
}

func TestUnitPlacementSelectsAfterPreviousObligationSettles(t *testing.T) {
	for _, state := range []string{service.UnitFailed, service.UnitUnconfirmed, service.UnitForming} {
		t.Run(state, func(t *testing.T) {
			fake := newFake()
			fake.runsByKey["old-key"] = "old-run"
			entered, release := make(chan struct{}, 1), make(chan struct{})
			fake.onDissolve = func() { entered <- struct{}{}; <-release }
			pins := service.DefaultUnitPins()
			pins.WorkerSolPlacements = map[string]tetherclient.DefinitionRef{
				"worker-1": pins.WorkerSolPlacements["worker-1"],
				"worker-2": pins.WorkerSolPlacements["worker-2"],
			}
			svc := unitSetup(t, fake, service.UnitConfig{Pins: pins, MaxActive: 8})
			replacing := newUnitTask(t, svc, nil)
			// Failed+unconfirmed takes confirmEnded; unconfirmed and exhausted forming
			// take settleStuck. Both must retain old custody during network settlement.
			fake.teamIDs = []string{fmt.Sprintf("unit-%s-a1", strings.ToLower(replacing))}
			fake.teamRun = []string{"old-run"}
			b, err := json.Marshal(service.UnitRun{State: state, Unconfirmed: state == service.UnitFailed, Placement: "worker-2", Workers: 1, WorkerRuntime: "sol", Key: "old-key", RunID: "old-run", Attempt: 1, Replays: 5})
			require.NoError(t, err)
			raw := string(b)
			require.NoError(t, svc.Store().UpdateTask(replacing, sqlstore.TaskUpdate{UnitRun: &raw}))
			result := make(chan error, 1)
			go func() { _, err := dispatch(svc, replacing); result <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("settlement did not start")
			}
			other := newUnitTask(t, svc, nil)
			_, err = dispatch(svc, other)
			close(release)
			require.NoError(t, err)
			require.NoError(t, <-result)
			require.Equal(t, "worker-1", unitRun(t, svc, other).Placement)
			require.Equal(t, "worker-2", unitRun(t, svc, replacing).Placement)
		})
	}
}
