package executor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockExecutor is a test double that simulates execution without real LLM calls.
// It supports configurable results, delays, errors, and event emission.
type MockExecutor struct {
	mu            sync.Mutex
	result        *ExecutionResult
	err           error
	validateErr   error
	validateFunc  func(job *ExecutionJob) error
	delay         time.Duration
	events        []ExecutionEvent
	recordedJobs  []*ExecutionJob
	validatedJobs []*ExecutionJob
	runCount      int
}

// NewMockExecutor creates a MockExecutor that returns success by default.
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{}
}

func (m *MockExecutor) Name() string { return "mock" }

func (m *MockExecutor) Run(ctx context.Context, job *ExecutionJob, cb EventCallback) (*ExecutionResult, error) {
	m.mu.Lock()
	m.recordedJobs = append(m.recordedJobs, job)
	m.runCount++
	delay := m.delay
	events := m.events
	result := m.result
	retErr := m.err
	m.mu.Unlock()

	// Simulate execution delay
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, fmt.Errorf("mock executor: %w", ctx.Err())
		}
	}

	// Emit configured events
	if cb != nil {
		for _, event := range events {
			cb(event)
		}
	}

	// Return configured error
	if retErr != nil {
		return nil, retErr
	}

	// Return configured or default result
	if result != nil {
		return result, nil
	}

	return &ExecutionResult{
		Status:   "done",
		Reason:   "mock execution completed",
		Cost:     0.01,
		Duration: delay,
		Tokens:   TokenUsage{PromptTokens: 100, CompletionTokens: 50},
	}, nil
}

func (m *MockExecutor) Capabilities() ExecutorCapabilities {
	return ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
}

func (m *MockExecutor) Validate(job *ExecutionJob) error {
	m.mu.Lock()
	m.validatedJobs = append(m.validatedJobs, job)
	vf := m.validateFunc
	verr := m.validateErr
	m.mu.Unlock()
	if vf != nil {
		return vf(job)
	}
	return verr
}

// SetValidateError configures Validate to return this error for every call.
// Use SetValidateFunc when a per-job decision is needed.
func (m *MockExecutor) SetValidateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateErr = err
	m.validateFunc = nil
}

// SetValidateFunc configures Validate to invoke fn with each job so tests
// can make per-job decisions (e.g. return PermanentError for unknown
// profiles only). Clears any previously set SetValidateError.
func (m *MockExecutor) SetValidateFunc(fn func(*ExecutionJob) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateFunc = fn
	m.validateErr = nil
}

// ValidatedJobs returns every job the scheduler submitted to Validate, in
// order. Useful for asserting that the pre-dispatch hook ran.
func (m *MockExecutor) ValidatedJobs() []*ExecutionJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*ExecutionJob, len(m.validatedJobs))
	copy(out, m.validatedJobs)
	return out
}

// RunCount returns how many times Run was invoked. Assertions use this to
// confirm a task that failed Validate was NEVER dispatched.
func (m *MockExecutor) RunCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runCount
}

// SetResult configures the result that Run will return.
func (m *MockExecutor) SetResult(result *ExecutionResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = result
}

// SetError configures Run to return this error.
func (m *MockExecutor) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// SetDelay configures Run to sleep for this duration before returning.
func (m *MockExecutor) SetDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

// SetEvents configures events that Run will emit via the EventCallback.
func (m *MockExecutor) SetEvents(events []ExecutionEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = events
}

// RecordedJobs returns all jobs that were passed to Run, in order.
func (m *MockExecutor) RecordedJobs() []*ExecutionJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*ExecutionJob, len(m.recordedJobs))
	copy(out, m.recordedJobs)
	return out
}

// Reset clears all configuration and recorded jobs.
func (m *MockExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = nil
	m.err = nil
	m.validateErr = nil
	m.validateFunc = nil
	m.delay = 0
	m.events = nil
	m.recordedJobs = nil
	m.validatedJobs = nil
	m.runCount = 0
}
