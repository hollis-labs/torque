package agent

// trackTurns has every turn Manager.SendTurn sends sessID open a turn on t
// before the send (turnTracker.beginSend), so the long-lived runtime never
// reads a session it just sent a turn as idle (CW-20261001-0117). The
// returned func stops it.
func (m *Manager) trackTurns(sessID string, t *turnTracker) (untrack func()) {
	if m == nil || sessID == "" || t == nil {
		return func() {}
	}
	m.turnTrackers.Store(sessID, t)
	return func() { m.turnTrackers.CompareAndDelete(sessID, t) }
}

// beginSentTurn opens a turn on sessID's tracker, if one is registered, and
// returns the abort for a failed send.
func (m *Manager) beginSentTurn(sessID string) (abort func()) {
	if m == nil {
		return func() {}
	}
	v, ok := m.turnTrackers.Load(sessID)
	if !ok {
		return func() {}
	}
	return v.(*turnTracker).beginSend()
}
