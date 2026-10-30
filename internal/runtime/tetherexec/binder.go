package tetherexec

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

// StoreBinder writes the run-to-session mapping through the state writer.
type StoreBinder struct{ Writer writeq.Writer }

func (b StoreBinder) BindRun(ctx context.Context, runID int64, tetherSessionID, launchID, workstreamID string) error {
	return b.Writer.Submit(ctx, "tether_bind_run", func(tx *sqlstore.WriteTx) error {
		return tx.BindRunTether(runID, tetherSessionID, launchID, workstreamID)
	})
}
