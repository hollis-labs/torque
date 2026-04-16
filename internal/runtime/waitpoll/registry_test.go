package waitpoll_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

type stubPredicate struct {
	typ    string
	result bool
}

func (s *stubPredicate) Type() string                         { return s.typ }
func (s *stubPredicate) Validate(_ map[string]any) error      { return nil }
func (s *stubPredicate) Evaluate(_ context.Context, _ map[string]any) (bool, error) {
	return s.result, nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := waitpoll.NewRegistry()
	p := &stubPredicate{typ: "stub", result: true}
	require.NoError(t, r.Register(p))

	got, ok := r.Get("stub")
	require.True(t, ok)
	assert.Equal(t, "stub", got.Type())

	done, err := got.Evaluate(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestRegistry_DuplicateFails(t *testing.T) {
	r := waitpoll.NewRegistry()
	require.NoError(t, r.Register(&stubPredicate{typ: "dup"}))
	err := r.Register(&stubPredicate{typ: "dup"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_UnknownLookup(t *testing.T) {
	r := waitpoll.NewRegistry()
	_, ok := r.Get("missing")
	assert.False(t, ok)
}

func TestRegistry_List(t *testing.T) {
	r := waitpoll.NewRegistry()
	require.NoError(t, r.Register(&stubPredicate{typ: "a"}))
	require.NoError(t, r.Register(&stubPredicate{typ: "b"}))
	list := r.List()
	assert.ElementsMatch(t, []string{"a", "b"}, list)
}
