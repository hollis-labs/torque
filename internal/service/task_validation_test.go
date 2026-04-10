package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
)

func TestUnlimitedConstantExists(t *testing.T) {
	assert.Equal(t, int64(-1), service.Unlimited)
}

func TestDeliverableTypeExists(t *testing.T) {
	d := service.Deliverable{
		Type:        "diff",
		Required:    true,
		Description: "Git diff or patch content",
	}
	assert.Equal(t, "diff", d.Type)
	assert.True(t, d.Required)
	assert.Equal(t, "Git diff or patch content", d.Description)
}
