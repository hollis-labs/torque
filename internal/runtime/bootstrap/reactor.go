package bootstrap

import (
	"context"
	"fmt"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/reactor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// Reactor wires the envelope dispatch table (CW-20260512-0061, sprint α.3)
// into the running daemon. Builds the adapters that bridge reactor.Deps to
// the daemon's CheckpointService / store / agent.Manager / broker.Broker
// / scheduler.EventBus / TaskService surfaces, constructs the dispatcher,
// starts a Loop subscribed on a system-wide (zero-Address) filter, and
// returns a closer the caller defers at shutdown.
//
// Per sprint-α D2 the dispatch table only routes four envelope kinds —
// escalation / status_update / request / handoff. Everything else routes
// to noop+log inside reactor.Dispatch.
//
// Returns (closer, error). closer is always non-nil — call at shutdown to
// drain the loop goroutine. On error the reactor was not started.
func Reactor(
	ctx context.Context,
	store *sqlstore.Store,
	brk *broker.Broker,
	svc *service.Service,
	sessions *agent.Manager,
	bus *scheduler.EventBus,
) (func(), error) {
	if store == nil || brk == nil || svc == nil {
		return func() {}, fmt.Errorf("reactor bootstrap: store, broker, and service are required")
	}

	deps := reactor.Deps{
		Checkpoints: &checkpointAdapter{svc: svc.Checkpoint},
		Blocker:     &storeBlockerAdapter{store: store},
		Sessions:    sessionStopAdapter{mgr: sessions},
		Fanout:      &brokerFanoutAdapter{brk: brk},
		Reassign:    &reassignAdapter{tasks: svc.Task},
		Notifier:    &busNotifierAdapter{bus: bus},
	}
	d := reactor.New(deps)
	// V0 production wiring: subscribe system-wide (zero Address) with an
	// empty Filter so every kind reaches Dispatch, which itself encodes
	// the four-kind table. Future sprints may scope per authority.
	loop := reactor.NewLoop(brk, d, gomsg.Address{}, gomsg.Filter{})
	if err := loop.Start(ctx); err != nil {
		return func() {}, fmt.Errorf("reactor loop start: %w", err)
	}
	return loop.Close, nil
}

// --- adapters ---------------------------------------------------------

// checkpointAdapter bridges reactor.CheckpointEmitter to
// service.CheckpointService.Emit. The reactor's V0 escalation->HITL path
// uses a system source ("reactor") so operator audits can filter by it.
type checkpointAdapter struct {
	svc *service.CheckpointService
}

func (a *checkpointAdapter) Emit(taskID, payloadJSON string) (string, error) {
	if a.svc == nil {
		return "", fmt.Errorf("checkpoint service not wired")
	}
	out, err := a.svc.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              "message", // V0: escalation -> generic message-shaped HITL checkpoint
		PayloadJSON:       payloadJSON,
		EmitterSourceType: "system",
		EmitterSourceRef:  "reactor/envelope-dispatch",
	})
	if err != nil {
		return "", err
	}
	return out.CorrelationID, nil
}

// storeBlockerAdapter bridges reactor.TaskBlocker to
// sqlstore.Store.TransitionTaskWithReason.
type storeBlockerAdapter struct {
	store *sqlstore.Store
}

func (a *storeBlockerAdapter) TransitionTaskWithReason(taskID, status, reason string) error {
	return a.store.TransitionTaskWithReason(taskID, status, reason)
}

// sessionStopAdapter bridges reactor.SessionStopper to agent.Manager.Stop.
// Nil mgr is tolerated — reactor degrades to noop when the session surface
// is unavailable (e.g. test deps building a partial daemon).
type sessionStopAdapter struct {
	mgr *agent.Manager
}

func (a sessionStopAdapter) Stop(ctx context.Context, sessionID string) error {
	if a.mgr == nil {
		return nil
	}
	return a.mgr.Stop(ctx, sessionID)
}

// brokerFanoutAdapter bridges reactor.PeerFanout to broker.Broker.Send.
type brokerFanoutAdapter struct {
	brk *broker.Broker
}

func (a *brokerFanoutAdapter) Send(ctx context.Context, env gomsg.Envelope) (gomsg.Envelope, error) {
	return a.brk.Send(ctx, env)
}

// reassignAdapter bridges reactor.AgentReassigner to
// service.TaskService.Update. V0 handoffs flip AgentProfile only — future
// kinds may extend (e.g. executor swap on handoff).
type reassignAdapter struct {
	tasks *service.TaskService
}

func (a *reassignAdapter) ReassignTask(taskID, profile string) error {
	if a.tasks == nil {
		return fmt.Errorf("task service not wired")
	}
	p := profile
	return a.tasks.Update(taskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{AgentProfile: &p},
	})
}

// busNotifierAdapter bridges reactor.OperatorNotifier to
// scheduler.EventBus.Publish. nil bus is tolerated for the same reasons
// AgentDeps tolerates nil bus.
type busNotifierAdapter struct {
	bus *scheduler.EventBus
}

func (a *busNotifierAdapter) Notify(eventType, taskID string, data map[string]any) {
	if a.bus == nil {
		return
	}
	a.bus.Publish(scheduler.SchedulerEvent{
		Type:   eventType,
		TaskID: taskID,
		Data:   data,
	})
}
