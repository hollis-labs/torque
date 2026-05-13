package agent

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func (d *Dependencies) CreateSession(ctx context.Context, rec *sqlstore.SessionRecord) error {
	if d == nil || d.Store == nil {
		return fmt.Errorf("agent deps: nil store")
	}
	if d.StateWriter == nil {
		return d.Store.CreateSession(rec)
	}
	return d.StateWriter.Submit(ctx, "agent_create_session", func(tx *sqlstore.WriteTx) error {
		return tx.CreateSession(rec)
	})
}

func (d *Dependencies) UpdateSessionState(ctx context.Context, id, state string, pid int, exit *int) error {
	if d == nil || d.Store == nil {
		return fmt.Errorf("agent deps: nil store")
	}
	if d.StateWriter == nil {
		return d.Store.UpdateSessionState(id, state, pid, exit)
	}
	return d.StateWriter.Submit(ctx, "agent_update_session_state", func(tx *sqlstore.WriteTx) error {
		return tx.UpdateSessionState(id, state, pid, exit)
	})
}

func (d *Dependencies) TouchSession(ctx context.Context, id string) error {
	if d == nil || d.Store == nil {
		return fmt.Errorf("agent deps: nil store")
	}
	if d.StateWriter == nil {
		return d.Store.TouchSession(id)
	}
	return d.StateWriter.Submit(ctx, "agent_touch_session", func(tx *sqlstore.WriteTx) error {
		return tx.TouchSession(id)
	})
}

func (d *Dependencies) UpdateSessionResumeHint(ctx context.Context, id string, hint []byte) error {
	if d == nil || d.Store == nil {
		return fmt.Errorf("agent deps: nil store")
	}
	if d.StateWriter == nil {
		return d.Store.UpdateSessionResumeHint(id, hint)
	}
	return d.StateWriter.Submit(ctx, "agent_update_session_resume_hint", func(tx *sqlstore.WriteTx) error {
		return tx.UpdateSessionResumeHint(id, hint)
	})
}

func (d *Dependencies) UpdateSessionMeta(ctx context.Context, id, metaJSON string) error {
	if d == nil || d.Store == nil {
		return fmt.Errorf("agent deps: nil store")
	}
	if d.StateWriter == nil {
		return d.Store.UpdateSessionMeta(id, metaJSON)
	}
	return d.StateWriter.Submit(ctx, "agent_update_session_meta", func(tx *sqlstore.WriteTx) error {
		return tx.UpdateSessionMeta(id, metaJSON)
	})
}
