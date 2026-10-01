package agent

import (
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// ResumeSupport is whether a runtime, in one mode, can continue an earlier
// conversation, and whether Torque makes it (CW-20261001-0174). The two
// differ: the go-providers registry declares what a runtime can do, and
// Torque resumes only the pairs it has wired (resumeWired).
type ResumeSupport struct {
	// Declared: the registry declares runtimes.CapResume for the runtime in
	// this mode.
	Declared bool
	// Wired: Torque resumes it. The launch takes the stored id: the
	// wrapper's launch template renders it into the CLI's resume argument,
	// or an ACP session sends it in session/load. Only a Wired resume is
	// ever attempted. What happens when the id is lost varies:
	//   - a subprocess launch fails its first turn with a SessionLostError,
	//     and ResumeSession and planstart boot fresh once instead;
	//   - a streaming-stdio launch is ready before its first turn can fail;
	//     Boot judges the resume from the CLI's stderr and first frame
	//     (resume_verdict.go) and fails with the same error, so the same
	//     fresh boot follows (CW-20261001-0202);
	//   - an ACP agent that does not advertise loadSession opens a new
	//     session without saying so, which Torque does not detect yet
	//     (CW-20261001-0202).
	Wired bool
	// NotWired says why a Declared resume is not Wired.
	NotWired string
}

// resumeWired is the allow-list of (runtime, mode) pairs whose resume
// Torque wires, and how the stored id reaches the agent. A resume the
// registry declares for any other pair, such as a mode a library bump
// adds, stays unwired until it is added here with a test that the id
// reaches the launch.
var resumeWired = map[launch.Key]string{
	{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}:      "--resume <id>",
	{Runtime: runtimes.Claude, Mode: runtimes.ModeSubprocessPerTurn}:   "--resume <id>",
	{Runtime: runtimes.OpenCode, Mode: runtimes.ModeSubprocessPerTurn}: "--session <id>",
	{Runtime: runtimes.OpenCode, Mode: runtimes.ModeACPStdio}:          "session/load",
	{Runtime: runtimes.Pi, Mode: runtimes.ModeACPStdio}:                "session/load",
}

// resumeNotWired says why Torque does not wire a resume the registry
// declares in a mode Torque launches, where a task tracks it. A resume
// Torque cannot make real must never be claimed: the session would start
// fresh while its caller believed it continued.
var resumeNotWired = map[launch.Key]string{
	{Runtime: runtimes.Codex, Mode: runtimes.ModeJSONRPCStdio}:            "codex app-server resumes with thread/resume, but Torque does not set agentkit's ResumeThreadID, persist the thread id, or fire a resumed session's kickoff (CW-20261001-0180)",
	{Runtime: runtimes.Antigravity, Mode: runtimes.ModeSubprocessPerTurn}: "agy starts a new conversation for an unknown id without saying so; Torque neither checks resume-keeps-id nor acts on session.lost (CW-20261001-0181)",
}

// Resume reports ResumeSupport for a provider (a registry runtime id or
// alias, in any case, as Boot accepts it) in the given kind, resolved as
// Boot resolves it: an empty kind is the runtime's registry default. An
// unknown provider gets the zero value.
func Resume(providerName string, kind RuntimeKind) ResumeSupport {
	desc, ok := registry.Lookup(providerName)
	if !ok {
		return ResumeSupport{}
	}
	mode := kind.Mode()
	if mode == "" {
		mode = desc.DefaultMode
	}
	if !desc.Has(mode, runtimes.CapResume) {
		return ResumeSupport{}
	}
	key := launch.Key{Runtime: desc.ID, Mode: mode}
	if !bootLaunches(providerName, key) {
		return ResumeSupport{Declared: true, NotWired: "Torque does not launch " + providerName + " in " + string(mode)}
	}
	if _, ok := resumeWired[key]; !ok {
		why := resumeNotWired[key]
		if why == "" {
			why = "Torque does not wire " + string(desc.ID) + "'s resume in " + string(mode)
		}
		return ResumeSupport{Declared: true, NotWired: why}
	}
	return ResumeSupport{Declared: true, Wired: true}
}

// bootLaunches mirrors selectRuntime's refusals for a provider the registry
// knows: the retired bare claude name, and a (runtime, mode) the wrapper
// does not drive.
func bootLaunches(providerName string, key launch.Key) bool {
	return providerName != "claude" && slices.Contains(launch.Supported(), key)
}

// GenuinelyResumable reports whether threading a stored provider session id
// through Options.ProviderSessionIDOverride really restores the earlier
// conversation for this provider and kind: Resume(...).Wired. Callers that
// add a resume on top of the provider-agnostic recovery pack (such as
// planstart.Redispatch) gate on it, so they never claim a resume that
// silently starts fresh.
func GenuinelyResumable(providerName string, kind RuntimeKind) bool {
	return Resume(providerName, kind).Wired
}

// SameRuntime reports whether two provider names are the same registry
// runtime (claude and claude-code are, in any case), so a session id one
// recorded is meaningful to the other.
func SameRuntime(a, b string) bool {
	da, okA := registry.Lookup(a)
	db, okB := registry.Lookup(b)
	return okA && okB && da.ID == db.ID
}

// ProviderCapabilities returns the static capability set for one provider in
// one runtime kind (the per-adapter answer the cli Executor.Capabilities()
// can't give because it's whole-executor scope). SupportsResume is
// Resume(provider, kind).Wired: derived from the go-providers registry's
// per-mode capabilities, narrowed to what Torque wires (CW-20261001-0174).
//
// The capability is read once per resume, never per turn, and is the
// canonical answer (sprint α decision D4: no per-call probes). The one
// fallback after it: a resume whose provider has lost the session boots
// fresh once (ResumeSession, planstart.Redispatch).
//
// A provider the registry does not know reports the zero value: it is not a
// known-good adapter and shouldn't appear capable of anything.
func ProviderCapabilities(providerName string, kind RuntimeKind) executor.ExecutorCapabilities {
	if _, ok := registry.Lookup(providerName); !ok {
		return executor.ExecutorCapabilities{}
	}
	// The cli executor's executor-wide flags hold across providers; the
	// only per-provider axis today is SupportsResume.
	return executor.ExecutorCapabilities{
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
		SupportsResume:      Resume(providerName, kind).Wired,
	}
}
