package tether

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secretToken = "tether-token-do-not-leak-1234567890"

func kindRequest(t *testing.T, seed int64, description string) judge.Request {
	sets, err := judge.LoadSets()
	require.NoError(t, err)
	return sets["task-kind"].BuildRequest(map[string]string{"title": "Fix the crash", "description": description}, seed)
}

func layerRequest(t *testing.T) judge.Request {
	sets, _ := judge.LoadSets()
	return sets["task-layer"].BuildRequest(map[string]string{"title": "Style it", "description": "components"}, 1)
}

func newDecider(srv *httptest.Server) *Decider {
	d := New(srv.URL, secretToken)
	d.Backoff = time.Millisecond
	return d
}

// ok writes a Tether-shaped success: {"response": {..., "decision": {...}, "usage": {...}}}.
func ok(w http.ResponseWriter, answers string, usage string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"response":{"provider":"jev","model":"jev-1.13.0","decision":{"answers":` + answers + `,"latency_ms":88,"request_id":"r-1"},"usage":` + usage + `,"route":{"provider":"jev","model":"jev-1.13.0"}}}`))
}

const oneKindAnswer = `{"kind":{"type":"choice","choice":"bug","probabilities":{"bug":1}}}`

func TestDecideSendsTheAsBuiltRequestAndReadsTheAnswers(t *testing.T) {
	var got map[string]any
	var auth, path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path, method = r.Header.Get("Authorization"), r.URL.Path, r.Method
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		ok(w, `{"kind":{"type":"choice","choice":"bug","probabilities":{"bug":0.7,"feature":0.1,"chore":0.05,"cleanup":0.05,"audit":0.04,"docs":0.03,"other":0.03}}}`,
			`{"input_tokens":412,"output_tokens":3,"estimated_cost_usd":0.0000173}`)
	}))
	defer srv.Close()

	resp, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "It crashes."))
	require.NoError(t, err)

	assert.Equal(t, "POST", method)
	assert.Equal(t, "/ai/decide", path)
	assert.Equal(t, "Bearer "+secretToken, auth)

	// {"request": {caller_id, model_hint, decision: {state, purpose, on_error, questions}}}
	req := got["request"].(map[string]any)
	assert.Equal(t, "torque", req["caller_id"])
	assert.Equal(t, "jev-1.13.0", req["model_hint"], "the model is pinned")
	dec := req["decision"].(map[string]any)
	assert.Equal(t, "task_enrichment", dec["purpose"])
	assert.Equal(t, "fail_open", dec["on_error"])
	assert.Equal(t, "Fix the crash", dec["state"].(map[string]any)["title"])
	qs := dec["questions"].([]any)
	require.Len(t, qs, 1)
	q := qs[0].(map[string]any)
	assert.Equal(t, "kind", q["key"])
	assert.Equal(t, "choice", q["type"])
	assert.Len(t, q["options"], 7)
	assert.NotContains(t, got, "decision", "nothing sits outside the request envelope")

	assert.Equal(t, "jev-1.13.0", resp.Model)
	assert.Equal(t, 412, resp.Usage.InputTokens)
	assert.InDelta(t, 0.0000173, resp.Usage.CostUSD, 1e-12, "the gateway's own cost estimate is kept")
	assert.Equal(t, 88*time.Millisecond, resp.Latency)
	a := resp.Answers["kind"]
	assert.Equal(t, "bug", a.Choice)
	assert.InDelta(t, judge.ChoiceConfidence(a.Probabilities), a.Confidence, 1e-9, "confidence is derived when none is sent")
}

func TestAPurposeIsRequiredAndNothingIsSentWithoutOne(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		ok(w, oneKindAnswer, `{}`)
	}))
	defer srv.Close()
	req := kindRequest(t, 1, "x")
	req.Purpose = ""
	_, err := newDecider(srv).Decide(context.Background(), req)
	require.ErrorIs(t, err, ErrNoPurpose)
	req.Purpose = "   "
	_, err = newDecider(srv).Decide(context.Background(), req)
	require.ErrorIs(t, err, ErrNoPurpose)
	assert.EqualValues(t, 0, atomic.LoadInt32(&calls), "a request the budget would reject is never sent")

	// Every embedded set carries a purpose, so Torque's own requests always pass the guard.
	sets, _ := judge.LoadSets()
	for id, s := range sets {
		assert.NotEmpty(t, s.BuildRequest(nil, 1).Purpose, id)
	}
}

func TestOptionOrderSurvivesToTheWire(t *testing.T) {
	var optionOrders [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Request struct {
				Decision struct {
					Questions []struct {
						Options []struct {
							Key string `json:"key"`
						} `json:"options"`
					} `json:"questions"`
				} `json:"decision"`
			} `json:"request"`
		}
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &req))
		var keys []string
		for _, o := range req.Request.Decision.Questions[0].Options {
			keys = append(keys, o.Key)
		}
		optionOrders = append(optionOrders, keys)
		ok(w, oneKindAnswer, `{}`)
	}))
	defer srv.Close()
	d := newDecider(srv)

	for seed := int64(1); seed <= 6; seed++ {
		req := kindRequest(t, seed, "x")
		_, err := d.Decide(context.Background(), req)
		require.NoError(t, err)
		var want []string
		for _, o := range req.Questions[0].Options {
			want = append(want, o.Key)
		}
		assert.Equal(t, want, optionOrders[len(optionOrders)-1], "the order we built is the order we sent (the model leans toward the first option)")
	}
	differs := false
	for _, o := range optionOrders[1:] {
		if strings.Join(o, ",") != strings.Join(optionOrders[0], ",") {
			differs = true
		}
	}
	assert.True(t, differs, "different seeds really do send different orders")
}

func TestNoulAnswersNeedNoConfidenceField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok(w, `{"frontend":{"type":"noul","noul":0.75},"backend":{"noul":0.1}}`, `{"input_tokens":282,"output_tokens":20}`)
	}))
	defer srv.Close()
	resp, err := newDecider(srv).Decide(context.Background(), layerRequest(t))
	require.NoError(t, err)
	assert.Equal(t, judge.Noul, resp.Answers["frontend"].Type)
	assert.Equal(t, judge.Noul, resp.Answers["backend"].Type, "a missing type is taken from the question")
	assert.InDelta(t, 0.75, resp.Answers["frontend"].Noul, 1e-9)
	assert.Equal(t, 282, resp.Usage.InputTokens)
}

func TestScoreConfidenceIsDerivedFromLevelProbabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok(w, `{"size":{"type":"score","score":1.4,"probabilities":{"0":0,"1":0.57,"2":0.43}}}`, `{}`)
	}))
	defer srv.Close()
	req := judge.Request{State: "s", Purpose: "p", Questions: []judge.Question{{Key: "size", Type: judge.Score, Instructions: "How big?", Levels: []string{"tiny", "small", "large"}}}}
	resp, err := newDecider(srv).Decide(context.Background(), req)
	require.NoError(t, err)
	assert.InDelta(t, 0.355, resp.Answers["size"].Confidence, 0.005)
}

// failure writes a Tether error envelope.
func failure(w http.ResponseWriter, status int, code, kind, message string) {
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]any{"code": code, "message": message}}
	if kind != "" {
		body["error"].(map[string]any)["details"] = map[string]any{"kind": kind, "on_error": "fail_open"}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func TestRetriesOnlyWhatCouldSucceedAndSaysWhyOtherwise(t *testing.T) {
	type tc struct {
		status  int
		code    string
		kind    string
		retried bool
	}
	cases := []tc{
		{429, "decision_failed", "rate_limited", true},
		{503, "decision_failed", "overloaded", true},
		{504, "decision_failed", "timeout", true},
		{503, "decision_failed", "circuit_open", false}, // waiting is the point of an open breaker
		{502, "decision_failed", "auth", false},
		{502, "decision_failed", "unavailable", false},
		{400, "invalid_request", "", false},
		{500, "internal_error", "", false}, // includes budget rejections
		{401, "forbidden", "", false},
	}
	for _, c := range cases {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			failure(w, c.status, c.code, c.kind, "provider said no")
		}))
		_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
		srv.Close()
		var te *Error
		require.ErrorAs(t, err, &te, "%+v", c)
		assert.Equal(t, c.status, te.Status)
		assert.Equal(t, c.code, te.Code)
		assert.Equal(t, c.kind, te.Kind)
		assert.Equal(t, c.retried, te.Retryable(), "%+v", c)
		if c.retried {
			assert.EqualValues(t, 3, atomic.LoadInt32(&calls), "%+v", c)
		} else {
			assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "%+v", c)
		}
		if c.kind != "" {
			assert.Contains(t, err.Error(), "("+c.kind+")")
		}
	}
}

func TestARateLimitThatClearsIsRetriedToSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			failure(w, 429, "decision_failed", "rate_limited", "slow down")
			return
		}
		ok(w, `{"kind":{"type":"choice","choice":"docs","probabilities":{"docs":1}}}`, `{}`)
	}))
	defer srv.Close()
	resp, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	require.NoError(t, err)
	assert.Equal(t, "docs", resp.Answers["kind"].Choice)
	assert.EqualValues(t, 3, atomic.LoadInt32(&calls))
}

func TestABudgetRejectionIsReportedWithItsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure(w, 500, "internal_error", "", "usage budget exhausted for purpose task_enrichment")
	}))
	defer srv.Close()
	_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage budget exhausted for purpose task_enrichment")
}

func TestAnAnswerThatDoesNotMatchTheQuestionsIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"missing answer":      `{}`,
		"wrong type":          `{"kind":{"type":"noul","noul":0.5}}`,
		"choice without pick": `{"kind":{"type":"choice","probabilities":{"bug":1}}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok(w, body, `{}`) }))
		_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
		srv.Close()
		assert.Error(t, err, name)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok(w, `{"frontend":{"type":"noul","noul":1.4},"backend":{"type":"noul","noul":0.2}}`, `{}`)
	}))
	_, err := newDecider(srv).Decide(context.Background(), layerRequest(t))
	srv.Close()
	assert.Error(t, err, "probability above 1")

	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>bad gateway</html>`)) }))
	_, err = newDecider(html).Decide(context.Background(), kindRequest(t, 1, "x"))
	html.Close()
	assert.Error(t, err, "not json")
}

func TestNeitherTheTokenNorTheTaskTextAppearsInAnError(t *testing.T) {
	// The server echoes the token and a credential back in its error message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure(w, 403, "forbidden", "", "bad token "+secretToken+" and key sk-"+strings.Repeat("a1B2", 8))
	}))
	defer srv.Close()
	_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "SECRET TASK TEXT must never be echoed"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretToken)
	assert.NotContains(t, err.Error(), "sk-a1B2")
	assert.NotContains(t, err.Error(), "SECRET TASK TEXT")
	assert.Contains(t, err.Error(), "[redacted]")

	// A transport failure also leaves the request out.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()
	d := New(url, secretToken)
	d.Attempts = 1
	_, err = d.Decide(context.Background(), kindRequest(t, 1, "SECRET TASK TEXT"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET TASK TEXT")
	assert.NotContains(t, err.Error(), secretToken)
}

func TestRetryAfterIsHonouredButCapped(t *testing.T) {
	var calls int32
	var firstAt, secondAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			firstAt = time.Now()
			w.Header().Set("Retry-After", "1")
			failure(w, 429, "decision_failed", "rate_limited", "slow")
		default:
			secondAt = time.Now()
			ok(w, oneKindAnswer, `{}`)
		}
	}))
	defer srv.Close()
	_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, secondAt.Sub(firstAt), 900*time.Millisecond, "waited the server's Retry-After, not just our backoff")
}

func TestCancelledContextStopsTheCallAndTheWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure(w, 429, "decision_failed", "rate_limited", "slow")
	}))
	defer srv.Close()
	d := newDecider(srv)
	d.Backoff = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.Decide(ctx, kindRequest(t, 1, "x"))
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "does not sit out an hour-long backoff")
}

func TestNewWithoutAURLIsNil(t *testing.T) {
	assert.Nil(t, New("", "t"))
	assert.Nil(t, New("   ", "t"))
	assert.Equal(t, "http://x:1", New("http://x:1/", "").BaseURL)
	assert.Equal(t, "tether-decide", (&Decider{}).Name())
}

func TestDecideOverTethersUnixSocket(t *testing.T) {
	// Short path: unix socket paths are limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "tsock")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "t.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var gotPath, gotHost string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.Path, r.Host
		ok(w, oneKindAnswer, `{"input_tokens":10}`)
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	d := New("unix://"+sock, "")
	require.NotNil(t, d)
	resp, err := d.Decide(context.Background(), kindRequest(t, 1, "x"))
	require.NoError(t, err)
	assert.Equal(t, "bug", resp.Answers["kind"].Choice)
	assert.Equal(t, "/ai/decide", gotPath)
	assert.Equal(t, "unix", gotHost)

	// A socket that is not there is an error that names no request text.
	gone := New("unix://"+filepath.Join(dir, "missing.sock"), "")
	gone.Attempts = 1
	_, err = gone.Decide(context.Background(), kindRequest(t, 1, "SECRET TASK TEXT"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET TASK TEXT")
}
