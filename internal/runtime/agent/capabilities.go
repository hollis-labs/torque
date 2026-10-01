package agent

import (
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// ResumeSupport is whether a runtime, in one mode, can continue an earlier
// conversation, and whether Torque makes it (CW-20261001-0174). The two
// differ: the go-providers registry declares what a runtime can do, and
// Torque resumes only where its boot path threads the stored provider
// session id into the launch and a lost id cannot pass for a resume.
type ResumeSupport struct {
	// Declared: the registry declares runtimes.CapResume for the runtime in
	// this mode.
	Declared bool
	// Wired: Torque resumes it. The launch takes the stored id (the
	// wrapper's launch template renders it into the CLI's resume argument;
	// an ACP session sends it in session/load), and a lost id fails rather
	// than starting fresh. Only a Wired resume is ever attempted.
	Wired bool
	// NotWired says why a Declared resume is not Wired.
	NotWired string
}

// resumeNotWired lists the runtimes whose registry descriptor declares
// resume in a mode Torque launches, but that Torque does not resume yet,
// and why. A resume Torque cannot make real must never be claimed: the
// session would start fresh while its caller believed it continued.
var resumeNotWired = map[launch.Key]string{
	{Runtime: runtimes.Codex, Mode: runtimes.ModeJSONRPCStdio}:            "codex app-server resumes with thread/resume, but Torque does not set agentkit's ResumeThreadID, persist the thread id, or fire a resumed session's kickoff (CW-20261001-0180)",
	{Runtime: runtimes.Antigravity, Mode: runtimes.ModeSubprocessPerTurn}: "agy starts a new conversation for an unknown id without saying so; Torque neither checks resume-keeps-id nor acts on session.lost (CW-20261001-0181)",
}

// Resume reports ResumeSupport for a provider (a registry runtime id or
// alias) in the given kind, resolved as Boot resolves it: an empty kind is
// the runtime's registry default. An unknown provider gets the zero value.
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
	if why, ok := resumeNotWired[key]; ok {
		return ResumeSupport{Declared: true, NotWired: why}
	}
	if !slices.Contains(config.LaunchableProviders(), providerName) || !slices.Contains(launch.Supported(), key) {
		return ResumeSupport{Declared: true, NotWired: "Torque does not launch " + providerName + " in " + string(mode)}
	}
	return ResumeSupport{Declared: true, Wired: true}
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

// sameRuntime reports whether two provider names are the same registry
// runtime (claude and claude-code are), so a session id one recorded is
// meaningful to the other.
func sameRuntime(a, b string) bool {
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
// Manager.ResumeSession and the checkpoint-response breadcrumb read it; the
// capability is read once per resume, never per turn, and is the canonical
// answer (sprint α decision D4: no per-call probes, no
// try-resume-then-fallback runtime detection).
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
