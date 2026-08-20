package pagination_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorEncodeDecodeRoundTrip(t *testing.T) {
	token := pagination.Encode("priority", "asc", "2", "CW-20260407-0002")
	require.NotEmpty(t, token)

	c, err := pagination.Decode(token)
	require.NoError(t, err)
	assert.Equal(t, "priority", c.SortBy)
	assert.Equal(t, "asc", c.SortDir)
	assert.Equal(t, "2", c.SortValue)
	assert.Equal(t, "CW-20260407-0002", c.ID)
}

func TestCursorDecodeRejectsMalformed(t *testing.T) {
	_, err := pagination.Decode("not-valid-base64!!!")
	require.Error(t, err)

	// Valid base64url but not JSON.
	_, err = pagination.Decode("bm90LWpzb24")
	require.Error(t, err)
}

func TestCursorDecodeRejectsMissingFields(t *testing.T) {
	// Encode with an empty id, which Decode should reject as missing a
	// required field.
	token := pagination.Encode("priority", "asc", "2", "")
	_, err := pagination.Decode(token)
	require.Error(t, err)
}

func TestCursorValidateMismatch(t *testing.T) {
	c, err := pagination.Decode(pagination.Encode("priority", "asc", "2", "CW-1"))
	require.NoError(t, err)

	require.NoError(t, c.Validate("priority", "asc"))

	err = c.Validate("status", "asc")
	require.Error(t, err)

	err = c.Validate("priority", "desc")
	require.Error(t, err)
}

func TestValidateSortBy(t *testing.T) {
	got, err := pagination.ValidateSortBy("Priority", "priority", "status", "updated_at", "created_at")
	require.NoError(t, err)
	assert.Equal(t, "priority", got)

	_, err = pagination.ValidateSortBy("bogus", "priority", "status")
	require.Error(t, err)
}

func TestValidateSortDir(t *testing.T) {
	got, err := pagination.ValidateSortDir("DESC")
	require.NoError(t, err)
	assert.Equal(t, "desc", got)

	_, err = pagination.ValidateSortDir("sideways")
	require.Error(t, err)
}
