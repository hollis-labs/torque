package service_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/service"
)

// TestRunBulk_PartialSuccess covers the shared shape every bulk_* verb sits
// on top of (PRIM-003): succeeded preserves input order with failures
// dropped, and failed carries the original typed error per id rather than
// a stringified message.
func TestRunBulk_PartialSuccess(t *testing.T) {
	boom := errors.New("boom")
	succeeded, failed := service.RunBulk([]string{"a", "b", "c"}, func(id string) error {
		if id == "b" {
			return boom
		}
		return nil
	})

	require.Equal(t, []string{"a", "c"}, succeeded)
	require.Len(t, failed, 1)
	require.Equal(t, "b", failed[0].ID)
	require.ErrorIs(t, failed[0].Err, boom)
}

func TestRunBulk_AllSucceed(t *testing.T) {
	succeeded, failed := service.RunBulk([]string{"a", "b"}, func(id string) error {
		return nil
	})
	require.Equal(t, []string{"a", "b"}, succeeded)
	require.Empty(t, failed)
}

func TestRunBulk_AllFail(t *testing.T) {
	boom := errors.New("boom")
	succeeded, failed := service.RunBulk([]string{"a", "b"}, func(id string) error {
		return boom
	})
	require.Empty(t, succeeded)
	require.Len(t, failed, 2)
	require.Equal(t, "a", failed[0].ID)
	require.Equal(t, "b", failed[1].ID)
}

func TestRunBulk_EmptyIDs(t *testing.T) {
	calls := 0
	succeeded, failed := service.RunBulk(nil, func(id string) error {
		calls++
		return nil
	})
	require.Equal(t, 0, calls)
	require.Empty(t, succeeded)
	require.Empty(t, failed)
}
