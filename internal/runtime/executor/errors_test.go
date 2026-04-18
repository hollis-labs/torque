package executor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPermanentErrorNil(t *testing.T) {
	assert.Nil(t, NewPermanentError(nil), "wrapping nil should return nil")
}

func TestPermanentErrorPreservesMessage(t *testing.T) {
	base := errors.New("profile \"x\" has neither command nor provider set")
	pe := NewPermanentError(base)
	assert.EqualError(t, pe, base.Error(), "wrapped error must surface original message verbatim")
}

func TestIsPermanentTrue(t *testing.T) {
	pe := NewPermanentError(errors.New("config-permanent"))
	assert.True(t, IsPermanent(pe))
}

func TestIsPermanentFalse(t *testing.T) {
	assert.False(t, IsPermanent(nil))
	assert.False(t, IsPermanent(errors.New("transient")))
}

func TestIsPermanentWrapped(t *testing.T) {
	inner := NewPermanentError(errors.New("inner-perm"))
	wrapped := fmt.Errorf("outer: %w", inner)
	assert.True(t, IsPermanent(wrapped), "IsPermanent must see through fmt.Errorf chains")
}

func TestPermanentErrorUnwrap(t *testing.T) {
	base := errors.New("cause")
	pe := NewPermanentError(base)
	assert.Same(t, base, errors.Unwrap(pe))
}
