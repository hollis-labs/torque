package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// A caller cannot forge the torque.resumed meta Boot stamps when a launch
// carries a provider session id (CW-20261001-0203): callerSessionMeta drops
// it with the other Torque-owned keys.
func TestCallerSessionMeta_StripsTorqueResumed(t *testing.T) {
	out := callerSessionMeta(map[string]string{
		"torque.resumed": "true", "torque.mode": "x", "role": "implementer",
	})
	assert.NotContains(t, out, "torque.resumed")
	assert.NotContains(t, out, "torque.mode")
	assert.Equal(t, "implementer", out["role"], "a caller's own keys stay")
}

// A session row's torque.resumed meta reads back as Session.Resumed, and
// only "true" does.
func TestSessionFromRecord_Resumed(t *testing.T) {
	assert.True(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{"torque.resumed":"true"}`}).Resumed)
	assert.False(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{"torque.resumed":"false"}`}).Resumed)
	assert.False(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{}`}).Resumed)
}
