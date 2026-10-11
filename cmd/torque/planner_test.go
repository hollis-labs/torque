package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerImportCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	data := filepath.Join(tmp, "data")
	require.NoError(t, os.Mkdir(data, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(data, "inbox.json"),
		[]byte(`{"schema":"portfolio/inbox@1","items":[{"id":"IN-001","title":"t","rev":2}]}`), 0o644))

	var out bytes.Buffer
	require.NoError(t, runPlannerImport(context.Background(), &out, data, false, false, false))
	assert.Contains(t, out.String(), "DRY RUN")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, false))
	assert.Contains(t, out.String(), "Verified")
	assert.Contains(t, out.String(), "features.planner is off")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, true))
	assert.Contains(t, out.String(), `"verified": true`)

	require.Error(t, runPlannerImport(context.Background(), &out, "", false, false, false))
}

func TestPlannerBoardCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	// The planner is off by default: the command says how to turn it on.
	var out bytes.Buffer
	err := runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "features.planner")

	// Enable it and add one inbox item through the same database.
	store, closeStore, err := openStoreForCLI()
	require.NoError(t, err)
	svc := service.New(store)
	require.NoError(t, svc.Feature.Enable("planner"))
	_, err = svc.Planner.InboxAdd(map[string]any{"title": "Look at the board"}, "", "tester")
	require.NoError(t, err)
	closeStore()

	out.Reset()
	require.NoError(t, runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{}, false))
	assert.Contains(t, out.String(), "DEPARTURES")
	assert.Contains(t, out.String(), "TRIAGE")
	assert.Contains(t, out.String(), "IN-001")

	out.Reset()
	require.NoError(t, runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{Limit: 5}, true))
	assert.Contains(t, out.String(), `"limit": 5`)
}

func TestJudgeCommands(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	run := func(args ...string) (string, error) {
		cmd := judgeCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.ExecuteContext(context.Background())
		return out.String(), err
	}

	out, err := run("sets")
	require.NoError(t, err)
	for _, want := range []string{"task-kind", "task-decision-state", "task-layer", "suggest>=0.80"} {
		assert.Contains(t, out, want)
	}

	// A task with a kind tag, so there is something to label.
	store, closeStore, err := openStoreForCLI()
	require.NoError(t, err)
	svc := service.New(store)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Fix the crash", Description: "It is broken: a bug that must be fixed.", Tags: []string{"bug"}})
	require.NoError(t, err)
	closeStore()

	out, err = run("eval", "--set", "task-kind", "--n", "10")
	require.NoError(t, err)
	assert.Contains(t, out, "Set task-kind v1")
	assert.Contains(t, out, "keyword-baseline")
	assert.Contains(t, out, "1 labeled")

	out, err = run("enrich", "--set", "task-kind", "--task", task.ID)
	require.NoError(t, err)
	assert.Contains(t, out, "Dry run")
	out, _ = run("ledger")
	assert.NotContains(t, out, task.ID, "a dry run records nothing")

	out, err = run("enrich", "--set", "task-kind", "--task", task.ID, "--record")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded as suggestions by keyword-baseline")
	out, err = run("ledger", "--subject", task.ID)
	require.NoError(t, err)
	assert.Contains(t, out, task.ID)
	assert.Contains(t, out, "suggested")

	_, err = run("eval", "--set", "task-kind", "--decider", "jev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CW-20261010-0041")
	_, err = run("eval", "--set", "nope")
	require.Error(t, err)
	_, err = run("eval")
	require.Error(t, err, "--set is required")
}

// fakeDecide is a stand-in for Tether's /ai/decide, in its as-built shape: it
// answers every question (the first option of a choice, 0.8 for a yes/no) and
// counts the requests.
func fakeDecide(t *testing.T, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		var env struct {
			Request struct {
				Decision struct {
					Purpose   string           `json:"purpose"`
					Questions []judge.Question `json:"questions"`
				} `json:"decision"`
			} `json:"request"`
		}
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &env))
		require.NotEmpty(t, env.Request.Decision.Purpose, "the budget rejects a request with no purpose")
		answers := map[string]judge.Answer{}
		for _, q := range env.Request.Decision.Questions {
			switch q.Type {
			case judge.Choice:
				probs := map[string]float64{}
				for _, o := range q.Options {
					probs[o.Key] = 0.1 / float64(len(q.Options)-1)
				}
				probs["bug"] = 0.9
				answers[q.Key] = judge.Answer{Type: judge.Choice, Choice: "bug", Probabilities: probs}
			default:
				answers[q.Key] = judge.Answer{Type: judge.Noul, Noul: 0.8}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{
			"provider": "jev", "model": "jev-1.13.0",
			"decision": map[string]any{"answers": answers, "latency_ms": 90, "request_id": "r"},
			"usage":    map[string]any{"input_tokens": 300, "output_tokens": 5, "estimated_cost_usd": 0.0000126},
		}})
	}))
}

func TestJudgeEvalEstimateGuardAndCaps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))
	var calls int32
	srv := fakeDecide(t, &calls)
	defer srv.Close()
	t.Setenv("TORQUE_TETHER_URL", srv.URL)
	t.Setenv("TORQUE_TETHER_TOKEN", "test-token-123456")

	store, closeStore, err := openStoreForCLI()
	require.NoError(t, err)
	svc := service.New(store)
	for i := 0; i < 12; i++ {
		_, err := svc.Task.Create(service.TaskCreateInput{Title: fmt.Sprintf("Fix crash %d", i), Description: "It is broken: a bug.", Tags: []string{"bug"}})
		require.NoError(t, err)
	}
	closeStore()

	run := func(args ...string) (string, error) {
		cmd := judgeCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.ExecuteContext(context.Background())
		return out.String(), err
	}

	// --estimate calls nothing, for any decider.
	out, err := run("eval", "--set", "task-kind", "--decider", "tether", "--estimate")
	require.NoError(t, err)
	assert.Contains(t, out, "Estimate for task-kind with 12 examples")
	assert.Contains(t, out, "24 calls (2 per example)")
	assert.Contains(t, out, "$")
	assert.EqualValues(t, 0, atomic.LoadInt32(&calls))

	// A paid decider shows the estimate and refuses without --yes.
	out, err = run("eval", "--set", "task-kind", "--decider", "tether")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
	assert.Contains(t, out, "Estimate for task-kind")
	assert.EqualValues(t, 0, atomic.LoadInt32(&calls), "nothing is asked without --yes")
	_, err = run("enrich", "--set", "task-kind", "--task", "CW-20260101-0001", "--decider", "tether")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")

	// With --yes it runs, concurrently, and can write the report.
	report := filepath.Join(tmp, "report.json")
	out, err = run("eval", "--set", "task-kind", "--decider", "tether", "--yes", "--concurrency", "4", "--out", report)
	require.NoError(t, err)
	assert.Contains(t, out, "tether-decide")
	assert.Contains(t, out, "jev-1.13.0")
	assert.Contains(t, out, "12 labeled")
	assert.EqualValues(t, 24, atomic.LoadInt32(&calls))
	b, err := os.ReadFile(report)
	require.NoError(t, err)
	var rep judge.Report
	require.NoError(t, json.Unmarshal(b, &rep))
	assert.Equal(t, "task-kind", rep.SetID)
	assert.Equal(t, 24, rep.Calls)
	assert.NotContains(t, string(b), "test-token-123456", "the token is never in a report")

	// --max-calls stops the run and the report says so.
	atomic.StoreInt32(&calls, 0)
	out, err = run("eval", "--set", "task-kind", "--decider", "tether", "--yes", "--max-calls", "6")
	require.NoError(t, err)
	assert.Contains(t, out, "TRUNCATED")
	assert.LessOrEqual(t, atomic.LoadInt32(&calls), int32(6))

	// Without the URL the paid decider says what is missing.
	t.Setenv("TORQUE_TETHER_URL", "")
	_, err = run("eval", "--set", "task-kind", "--decider", "tether", "--estimate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TORQUE_TETHER_URL")
}
