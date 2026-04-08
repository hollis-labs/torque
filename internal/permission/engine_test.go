package permission

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineYoloMode(t *testing.T) {
	e := NewEngine(ModeYolo, nil)
	result := e.Check(context.Background(), "sess1", "rm", map[string]any{"command": "rm -rf /"}, ToolMeta{IsDestructive: true})
	assert.Equal(t, DecisionAllow, result.Decision)
}

func TestEnginePlanModeBlocksWrites(t *testing.T) {
	e := NewEngine(ModePlan, nil)

	// Write should be denied.
	result := e.Check(context.Background(), "sess1", "bash", map[string]any{}, ToolMeta{IsReadOnly: false})
	assert.Equal(t, DecisionDeny, result.Decision)

	// Read should be allowed.
	result = e.Check(context.Background(), "sess1", "bash", map[string]any{}, ToolMeta{IsReadOnly: true})
	assert.Equal(t, DecisionAllow, result.Decision)
}

func TestEngineDefaultModeAsksDestructive(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	result := e.Check(context.Background(), "sess1", "bash", map[string]any{}, ToolMeta{IsDestructive: true})
	assert.Equal(t, DecisionAsk, result.Decision)
}

func TestEngineAcceptEditsMode(t *testing.T) {
	e := NewEngine(ModeAcceptEdits, nil)

	// File edits auto-allowed.
	result := e.Check(context.Background(), "sess1", "dev_edit", map[string]any{}, ToolMeta{})
	assert.Equal(t, DecisionAllow, result.Decision)

	// Destructive asks.
	result = e.Check(context.Background(), "sess1", "bash", map[string]any{}, ToolMeta{IsDestructive: true})
	assert.Equal(t, DecisionAsk, result.Decision)

	// Non-edit non-read-only asks.
	result = e.Check(context.Background(), "sess1", "bash", map[string]any{}, ToolMeta{IsReadOnly: false, IsDestructive: false})
	assert.Equal(t, DecisionAsk, result.Decision)

	// Read-only allowed.
	result = e.Check(context.Background(), "sess1", "cat", map[string]any{}, ToolMeta{IsReadOnly: true})
	assert.Equal(t, DecisionAllow, result.Decision)
}

func TestEngineRuleOverrideDeny(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "rm", Behavior: DecisionDeny},
		},
	}
	e := NewEngine(ModeDefault, rules)
	result := e.Check(context.Background(), "sess1", "rm", nil, ToolMeta{IsReadOnly: false})
	assert.Equal(t, DecisionDeny, result.Decision)
}

func TestEngineRuleOverrideAllow(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "bash", Behavior: DecisionAllow},
		},
	}
	e := NewEngine(ModeDefault, rules)
	// bash is destructive by meta but rule overrides to allow.
	result := e.Check(context.Background(), "sess1", "bash", nil, ToolMeta{IsDestructive: true})
	assert.Equal(t, DecisionAllow, result.Decision)
}

func TestEngineRuleOverrideAsk(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "cat", Behavior: DecisionAsk},
		},
	}
	e := NewEngine(ModeDefault, rules)
	// cat is read-only but rule overrides to ask.
	result := e.Check(context.Background(), "sess1", "cat", nil, ToolMeta{IsReadOnly: true})
	assert.Equal(t, DecisionAsk, result.Decision)
}

func TestEngineSessionGrants(t *testing.T) {
	e := NewEngine(ModeDefault, nil)

	// Manually inject a session grant.
	e.mu.Lock()
	e.sessionGrants["sess1"] = map[string]Decision{"bash": DecisionAllow}
	e.mu.Unlock()

	result := e.Check(context.Background(), "sess1", "bash", nil, ToolMeta{IsDestructive: true})
	assert.Equal(t, DecisionAllow, result.Decision)
	assert.Equal(t, "session grant", result.Reason)
}

func TestEngineDenyRulePriority(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "*", Behavior: DecisionAllow},
			{Tool: "rm", Behavior: DecisionDeny},
		},
	}
	e := NewEngine(ModeDefault, rules)
	result := e.Check(context.Background(), "sess1", "rm", nil, ToolMeta{})
	assert.Equal(t, DecisionDeny, result.Decision)
}

func TestEngineSetModeThreadSafe(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.SetMode(ModeYolo)
		}()
		go func() {
			defer wg.Done()
			_ = e.Mode()
		}()
	}
	wg.Wait()
}

func TestEngineSetRules(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "bash", Behavior: DecisionDeny},
		},
	}
	e.SetRules(rules)

	result := e.Check(context.Background(), "sess1", "bash", nil, ToolMeta{IsReadOnly: true})
	assert.Equal(t, DecisionDeny, result.Decision)
}

func TestEngineApprovalFlowRespondWithSessionScope(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(5 * time.Second)

	req := e.RequestApproval("sess1", "bash", nil, "needs approval")
	require.NotEmpty(t, req.ID)
	assert.Equal(t, "sess1", req.SessionID)

	// Respond in goroutine.
	go func() {
		time.Sleep(10 * time.Millisecond)
		ok := e.Respond(req.ID, DecisionAllow, ScopeSession, "sess1")
		assert.True(t, ok)
	}()

	resp := e.WaitForApproval(context.Background(), req)
	assert.Equal(t, DecisionAllow, resp.Decision)
	assert.False(t, resp.TimedOut)

	// Session grant should be recorded.
	e.mu.RLock()
	grants := e.sessionGrants["sess1"]
	e.mu.RUnlock()
	assert.Equal(t, DecisionAllow, grants["bash"])
}

func TestEngineApprovalTimeout(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(50 * time.Millisecond)

	req := e.RequestApproval("sess1", "bash", nil, "will timeout")
	resp := e.WaitForApproval(context.Background(), req)
	assert.Equal(t, DecisionDeny, resp.Decision)
	assert.True(t, resp.TimedOut)
}

func TestEngineApprovalContextCancel(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	req := e.RequestApproval("sess1", "bash", nil, "will be cancelled")

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	resp := e.WaitForApproval(ctx, req)
	assert.Equal(t, DecisionDeny, resp.Decision)
	assert.False(t, resp.TimedOut)
}

func TestEngineRespondToExpired(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(30 * time.Millisecond)

	req := e.RequestApproval("sess1", "bash", nil, "will expire")
	// Wait for timeout.
	resp := e.WaitForApproval(context.Background(), req)
	assert.True(t, resp.TimedOut)

	// Responding after timeout should return false.
	ok := e.Respond(req.ID, DecisionAllow, ScopeOnce, "sess1")
	assert.False(t, ok)
}

func TestEngineRespondWrongSession(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(5 * time.Second)

	req := e.RequestApproval("sess1", "bash", nil, "wrong session test")

	// Wrong session should fail.
	ok := e.Respond(req.ID, DecisionAllow, ScopeOnce, "sess2")
	assert.False(t, ok)

	// Clean up — drain the channel to avoid goroutine leak.
	go func() {
		e.Respond(req.ID, DecisionDeny, ScopeOnce, "sess1")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e.WaitForApproval(ctx, req)
}

func TestEngineClearSessionGrants(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.mu.Lock()
	e.sessionGrants["sess1"] = map[string]Decision{"bash": DecisionAllow}
	e.mu.Unlock()

	e.ClearSessionGrants("sess1")

	e.mu.RLock()
	_, exists := e.sessionGrants["sess1"]
	e.mu.RUnlock()
	assert.False(t, exists)
}
