package plugin

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterConstants(t *testing.T) {
	assert.Equal(t, "task.before_create", FilterTaskBeforeCreate)
	assert.Equal(t, "task.before_transition", FilterTaskBeforeTransition)
	assert.Equal(t, "execution.before_run", FilterExecutionBeforeRun)
	assert.Equal(t, "artifact.before_attach", FilterArtifactBeforeAttach)
}

func TestFilterPassthrough(t *testing.T) {
	r := NewFilterRegistry()
	ctx := FilterContext{TaskID: "t1"}
	out, err := r.Apply(FilterTaskBeforeCreate, "hello", ctx)
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

func TestFilterChainOrdering(t *testing.T) {
	r := NewFilterRegistry()
	var order []int

	r.Register("chain", "plugin-a", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, 10)
		return data, nil
	})
	r.Register("chain", "plugin-b", 1, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, 1)
		return data, nil
	})
	r.Register("chain", "plugin-c", 5, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, 5)
		return data, nil
	})

	_, err := r.Apply("chain", nil, FilterContext{})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 5, 10}, order)
}

func TestFilterAbort(t *testing.T) {
	r := NewFilterRegistry()

	r.Register("chain", "plugin-a", 1, func(data interface{}, ctx FilterContext) (interface{}, error) {
		return nil, fmt.Errorf("abort")
	})
	r.Register("chain", "plugin-b", 2, func(data interface{}, ctx FilterContext) (interface{}, error) {
		t.Fatal("should not be called")
		return data, nil
	})

	_, err := r.Apply("chain", "input", FilterContext{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "abort")
}

func TestFilterRemoveByPlugin(t *testing.T) {
	r := NewFilterRegistry()
	noop := func(data interface{}, ctx FilterContext) (interface{}, error) { return data, nil }

	r.Register("a", "plugin-x", 1, noop)
	r.Register("a", "plugin-y", 2, noop)
	r.Register("b", "plugin-x", 3, noop)

	removed := r.RemoveByPlugin("plugin-x")
	assert.Equal(t, 2, removed)
	assert.Equal(t, 1, r.Len("a"))
	assert.Equal(t, 0, r.Len("b"))
}

func TestFilterLen(t *testing.T) {
	r := NewFilterRegistry()
	assert.Equal(t, 0, r.Len("missing"))

	noop := func(data interface{}, ctx FilterContext) (interface{}, error) { return data, nil }
	r.Register("x", "p", 1, noop)
	r.Register("x", "p", 2, noop)
	assert.Equal(t, 2, r.Len("x"))
}
