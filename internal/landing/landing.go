// Package landing decides whether a task has landed: its work is merged and
// its configured checks passed. A finished task or an exited process does not
// prove work shipped, so the done transition asks this package first.
//
// Evidence lives in task metadata under the "landing" key and is read here,
// never written. Source-control state comes from a SourceControl adapter so
// the GitHub plugin (CW-20261010-0222) can replace the gh-backed one.
package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GateCIGreen is the quality gate that requires the PR's checks to pass, or
// author-pasted gate output when the task is configured to accept it.
const GateCIGreen = "ci_green"

// ModeAuthorPasted accepts Evidence.GateOutput in place of CI results. GitHub
// CI often has no credits, so this is a configured mode, not a loophole: the
// task must opt in through Evidence.GateMode.
const ModeAuthorPasted = "author_pasted"

// PRState is the source-control state of one pull request.
type PRState struct {
	URL    string
	State  string // OPEN, MERGED, CLOSED
	Checks []Check
}

// Check is one CI check result. Conclusion is upper-case (SUCCESS, FAILURE...)
// and empty while the check is still running.
type Check struct {
	Name       string
	Conclusion string
}

// SourceControl reads pull-request state. It never mutates.
type SourceControl interface {
	PR(ctx context.Context, ref string) (PRState, error)
}

// Evidence is the "landing" object in task metadata.
type Evidence struct {
	PRURL      string `json:"pr_url"`
	WaivedBy   string `json:"waived_by"`
	WaiveWhy   string `json:"waive_reason"`
	GateMode   string `json:"gate_mode"`
	GateOutput string `json:"gate_output"`
}

// Policy is what the task asks for.
type Policy struct {
	OnDoneMerge  string   // none, auto, pr, auto-resolve
	QualityGates []string // only GateCIGreen is enforced here
}

// Required reports whether the policy asks for any landing evidence.
func (p Policy) Required() bool {
	return (p.OnDoneMerge != "" && p.OnDoneMerge != "none") || p.hasGate(GateCIGreen)
}

func (p Policy) hasGate(g string) bool {
	for _, x := range p.QualityGates {
		if x == g {
			return true
		}
	}
	return false
}

// ParseEvidence extracts the landing object from a task's metadata JSON.
// Missing or malformed metadata yields empty evidence.
func ParseEvidence(metadata string) Evidence {
	var m struct {
		Landing Evidence `json:"landing"`
	}
	if metadata != "" {
		_ = json.Unmarshal([]byte(metadata), &m)
	}
	return m.Landing
}

// Verdict is the outcome of Evaluate. Missing names each unmet requirement
// with its remedy, so the caller can surface it verbatim.
type Verdict struct {
	OK      bool
	Waived  bool
	Missing []string
	// Undecided is set when nothing is known to be wrong but the gate could
	// not decide yet: the PR or its checks could not be read, or the checks
	// have not finished. It is false as soon as any requirement is plainly
	// unmet, and meaningless when OK.
	Undecided bool
}

// Reason joins Missing for a blocked reason.
func (v Verdict) Reason() string {
	return "landing evidence missing: " + strings.Join(v.Missing, "; ")
}

// Evaluate checks the policy against the evidence and source-control state.
// A source-control read failure is a missing requirement, never a pass.
func Evaluate(ctx context.Context, sc SourceControl, p Policy, ev Evidence) Verdict {
	if !p.Required() {
		return Verdict{OK: true}
	}
	if ev.WaivedBy != "" && ev.WaiveWhy != "" {
		return Verdict{OK: true, Waived: true}
	}
	var missing []string
	soft := 0 // missing entries that are "could not decide", not "unmet"

	needPR := p.OnDoneMerge != "" && p.OnDoneMerge != "none"
	needCI := p.hasGate(GateCIGreen)
	var pr PRState
	havePR := false
	if needPR || (needCI && ev.GateMode != ModeAuthorPasted) {
		switch {
		case ev.PRURL == "":
			missing = append(missing, `no PR recorded (set metadata.landing.pr_url, or waive with landing.waived_by and landing.waive_reason)`)
		case sc == nil:
			missing = append(missing, "no source-control adapter configured")
			soft++
		default:
			s, err := sc.PR(ctx, ev.PRURL)
			if err != nil {
				missing = append(missing, fmt.Sprintf("cannot read PR %s: %v", ev.PRURL, err))
				soft++
			} else {
				pr, havePR = s, true
			}
		}
	}
	if needPR && havePR && !strings.EqualFold(pr.State, "MERGED") {
		missing = append(missing, fmt.Sprintf("PR %s is %s, not MERGED", ev.PRURL, strings.ToUpper(pr.State)))
	}
	if needCI {
		if ev.GateMode == ModeAuthorPasted {
			if strings.TrimSpace(ev.GateOutput) == "" {
				missing = append(missing, "gate_mode is author_pasted but landing.gate_output is empty")
			}
		} else if havePR {
			failing, pending := failingChecks(pr)
			missing = append(missing, failing...)
			soft += pending
		}
	}
	return Verdict{OK: len(missing) == 0, Missing: missing, Undecided: len(missing) > 0 && soft == len(missing)}
}

// failingChecks lists the unmet CI requirements and how many of them are only
// "not finished yet" rather than a failed check. No checks at all is unmet, not
// pending: nothing is running that could turn it green.
func failingChecks(pr PRState) (out []string, pending int) {
	if len(pr.Checks) == 0 {
		return []string{fmt.Sprintf("PR %s has no CI checks (use landing.gate_mode=author_pasted with landing.gate_output if CI is unavailable)", pr.URL)}, 0
	}
	for _, c := range pr.Checks {
		switch c.Conclusion {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		case "":
			out = append(out, fmt.Sprintf("check %s has not finished", c.Name))
			pending++
		default:
			out = append(out, fmt.Sprintf("check %s concluded %s", c.Name, c.Conclusion))
		}
	}
	return out, pending
}
