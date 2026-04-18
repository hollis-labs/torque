package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCancelRegistry_RegisterDeregister covers the happy-path lifecycle:
// register a cancel, deregister, confirm size is 0. Also confirms
// deregister is idempotent (second call returns false).
func TestCancelRegistry_RegisterDeregister(t *testing.T) {
	r := newCancelRegistry()
	require.Equal(t, 0, r.size())

	_, cancel := context.WithCancel(context.Background())
	r.register("CW-1", cancel)
	require.Equal(t, 1, r.size())

	require.True(t, r.deregister("CW-1"))
	require.Equal(t, 0, r.size())

	// Second deregister is a no-op.
	require.False(t, r.deregister("CW-1"))
}

// TestCancelRegistry_Cancel fires the cancel func and removes the entry in a
// single call. The associated context must see its Done channel close.
func TestCancelRegistry_Cancel(t *testing.T) {
	r := newCancelRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	r.register("CW-1", cancel)

	require.True(t, r.cancel("CW-1"))
	require.Equal(t, 0, r.size())

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled within 1s")
	}

	// Second cancel is a no-op — the CancelFunc itself is idempotent
	// (stdlib guarantees) and the registry has no entry to dispatch.
	require.False(t, r.cancel("CW-1"))
}

// TestCancelRegistry_DoubleRegister confirms that registering a second
// cancel for the same taskID invokes the previous one so no context
// leaks if a future caller accidentally double-registers.
func TestCancelRegistry_DoubleRegister(t *testing.T) {
	r := newCancelRegistry()

	var firstFired int32
	_, cancel1 := context.WithCancel(context.Background())
	wrappedFirst := func() {
		atomic.StoreInt32(&firstFired, 1)
		cancel1()
	}
	r.register("CW-1", wrappedFirst)

	_, cancel2 := context.WithCancel(context.Background())
	r.register("CW-1", cancel2)

	require.Equal(t, int32(1), atomic.LoadInt32(&firstFired),
		"first cancel should fire when second is registered under same key")
	require.Equal(t, 1, r.size())
}

// TestCancelRegistry_CancelAll fires every registered cancel and clears
// the map. Shutdown path relies on this.
func TestCancelRegistry_CancelAll(t *testing.T) {
	r := newCancelRegistry()
	var wg sync.WaitGroup

	ctxs := make([]context.Context, 5)
	for i := range ctxs {
		c, cf := context.WithCancel(context.Background())
		ctxs[i] = c
		r.register(idFor(i), cf)
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			<-ctx.Done()
		}(c)
	}
	require.Equal(t, 5, r.size())

	r.cancelAll()
	require.Equal(t, 0, r.size())

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("not all contexts cancelled within 1s")
	}
}

// TestMergeContexts_EitherParentCancels exercises both cancellation paths
// of the mergeContexts helper.
func TestMergeContexts_ParentACancels(t *testing.T) {
	a, cancelA := context.WithCancel(context.Background())
	b, cancelB := context.WithCancel(context.Background())
	defer cancelB()

	merged, cancelMerged := mergeContexts(a, b)
	defer cancelMerged()

	cancelA()
	select {
	case <-merged.Done():
	case <-time.After(time.Second):
		t.Fatal("merged ctx did not cancel when a cancelled")
	}
}

func TestMergeContexts_ParentBCancels(t *testing.T) {
	a, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	b, cancelB := context.WithCancel(context.Background())

	merged, cancelMerged := mergeContexts(a, b)
	defer cancelMerged()

	cancelB()
	select {
	case <-merged.Done():
	case <-time.After(time.Second):
		t.Fatal("merged ctx did not cancel when b cancelled")
	}
}

func idFor(i int) string {
	return "CW-" + string(rune('A'+i))
}
