package tether

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestDecideSendsTheContractAndReadsTheAnswers(t *testing.T) {
	var got map[string]any
	var auth, path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path, method = r.Header.Get("Authorization"), r.URL.Path, r.Method
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"kind":{"type":"choice","choice":"bug","probabilities":{"bug":0.7,"feature":0.1,"chore":0.05,"cleanup":0.05,"audit":0.04,"docs":0.03,"other":0.03}}},
			"usage":{"input_tokens":412,"output_tokens":3},"provider":"jev","model":"jev-1.13.0","latency_ms":88,"request_id":"r-1"}`))
	}))
	defer srv.Close()

	resp, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "It crashes."))
	require.NoError(t, err)

	assert.Equal(t, "POST", method)
	assert.Equal(t, "/ai/decide", path)
	assert.Equal(t, "Bearer "+secretToken, auth)
	assert.Equal(t, "jev-1.13.0", got["model"], "the model is pinned")
	assert.Equal(t, "task_enrichment", got["purpose"])
	assert.Equal(t, "torque", got["caller_id"])
	assert.Equal(t, "fail_open", got["on_error"])
	qs := got["questions"].([]any)
	require.Len(t, qs, 1)
	assert.Equal(t, "kind", qs[0].(map[string]any)["key"])

	assert.Equal(t, "jev-1.13.0", resp.Model)
	assert.Equal(t, 412, resp.Usage.InputTokens)
	assert.Equal(t, 88*time.Millisecond, resp.Latency)
	a := resp.Answers["kind"]
	assert.Equal(t, "bug", a.Choice)
	assert.InDelta(t, judge.ChoiceConfidence(a.Probabilities), a.Confidence, 1e-9, "confidence is derived when the provider sends none")
}

func TestOptionOrderSurvivesToTheWire(t *testing.T) {
	var optionOrders [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions []struct {
				Options []struct {
					Key string `json:"key"`
				} `json:"options"`
			} `json:"questions"`
		}
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &req))
		var keys []string
		for _, o := range req.Questions[0].Options {
			keys = append(keys, o.Key)
		}
		optionOrders = append(optionOrders, keys)
		_, _ = w.Write([]byte(`{"answers":{"kind":{"type":"choice","choice":"bug","probabilities":{"bug":1}}},"usage":{}}`))
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
		_, _ = w.Write([]byte(`{"answers":{"frontend":{"type":"noul","noul":0.75},"backend":{"noul":0.1}},"usage":{"input_tokens":282,"output_tokens":20},"model":"jev-1.13.0"}`))
	}))
	defer srv.Close()
	resp, err := newDecider(srv).Decide(context.Background(), layerRequest(t))
	require.NoError(t, err)
	assert.Equal(t, judge.Noul, resp.Answers["frontend"].Type)
	assert.Equal(t, judge.Noul, resp.Answers["backend"].Type, "a missing type is taken from the question")
	assert.InDelta(t, 0.75, resp.Answers["frontend"].Noul, 1e-9)
	assert.Equal(t, 282, resp.Usage.InputTokens)
	assert.Positive(t, resp.Latency, "falls back to the measured round trip when none is reported")
}

func TestRetriesRateLimitsButNotClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"answers":{"kind":{"type":"choice","choice":"docs","probabilities":{"docs":1}}},"usage":{}}`))
	}))
	defer srv.Close()
	resp, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	require.NoError(t, err)
	assert.Equal(t, "docs", resp.Answers["kind"].Choice)
	assert.EqualValues(t, 3, atomic.LoadInt32(&calls))

	for _, status := range []int{401, 403, 422, 400, 404, 500} {
		atomic.StoreInt32(&calls, 0)
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"field questions.kind is wrong"}}`))
		}))
		_, err := newDecider(bad).Decide(context.Background(), kindRequest(t, 1, "x"))
		bad.Close()
		require.Error(t, err, status)
		var te *Error
		require.ErrorAs(t, err, &te)
		assert.Equal(t, status, te.Status)
		assert.Contains(t, te.Message, "field questions.kind is wrong")
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "status %d is not retried", status)
	}
}

func TestGivesUpAfterTheAttemptsAndReportsTheLastError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(529)
	}))
	defer srv.Close()
	_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	var te *Error
	require.ErrorAs(t, err, &te)
	assert.Equal(t, 529, te.Status)
	assert.True(t, te.Retryable())
	assert.EqualValues(t, 3, atomic.LoadInt32(&calls))
}

func TestAnAnswerThatDoesNotMatchTheQuestionsIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"missing answer":      `{"answers":{},"usage":{}}`,
		"wrong type":          `{"answers":{"kind":{"type":"noul","noul":0.5}},"usage":{}}`,
		"choice without pick": `{"answers":{"kind":{"type":"choice","probabilities":{"bug":1}}},"usage":{}}`,
		"probability above 1": `{"answers":{"frontend":{"type":"noul","noul":1.4},"backend":{"type":"noul","noul":0.2}},"usage":{}}`,
		"not json":            `<html>bad gateway</html>`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		req := kindRequest(t, 1, "x")
		if name == "probability above 1" {
			req = layerRequest(t)
		}
		_, err := newDecider(srv).Decide(context.Background(), req)
		srv.Close()
		assert.Error(t, err, name)
	}
}

func TestNeitherTheTokenNorTheTaskTextAppearsInAnError(t *testing.T) {
	// The server echoes the token and a credential back in its error body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"bad token ` + secretToken + ` and key sk-` + strings.Repeat("a1B2", 8) + `"}`))
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
			w.WriteHeader(429)
		default:
			secondAt = time.Now()
			_, _ = w.Write([]byte(`{"answers":{"kind":{"type":"choice","choice":"bug","probabilities":{"bug":1}}},"usage":{}}`))
		}
	}))
	defer srv.Close()
	_, err := newDecider(srv).Decide(context.Background(), kindRequest(t, 1, "x"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, secondAt.Sub(firstAt), 900*time.Millisecond, "waited the server's Retry-After, not just our backoff")
}

func TestCancelledContextStopsTheCallAndTheWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
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
