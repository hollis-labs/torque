package landing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSC struct {
	pr  PRState
	err error
}

func (f fakeSC) PR(context.Context, string) (PRState, error) { return f.pr, f.err }

func TestEvaluate(t *testing.T) {
	merged := PRState{URL: "u", State: "MERGED", Checks: []Check{{"build", "SUCCESS"}}}
	cases := []struct {
		name string
		sc   SourceControl
		p    Policy
		ev   Evidence
		ok   bool
		want string
	}{
		{"not required", nil, Policy{OnDoneMerge: "none"}, Evidence{}, true, ""},
		{"no pr", fakeSC{}, Policy{OnDoneMerge: "pr"}, Evidence{}, false, "no PR recorded"},
		{"open pr", fakeSC{pr: PRState{State: "OPEN"}}, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, false, "not MERGED"},
		{"merged", fakeSC{pr: merged}, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, true, ""},
		{"gh error", fakeSC{err: errors.New("boom")}, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, false, "cannot read PR"},
		{"waived", nil, Policy{OnDoneMerge: "pr"}, Evidence{WaivedBy: "chris", WaiveWhy: "docs only"}, true, ""},
		{"waive needs reason", nil, Policy{OnDoneMerge: "pr"}, Evidence{WaivedBy: "chris"}, false, "no PR recorded"},
		{"ci red", fakeSC{pr: PRState{State: "MERGED", Checks: []Check{{"build", "FAILURE"}}}}, Policy{QualityGates: []string{GateCIGreen}}, Evidence{PRURL: "u"}, false, "build concluded FAILURE"},
		{"ci pending", fakeSC{pr: PRState{State: "OPEN", Checks: []Check{{"build", ""}}}}, Policy{QualityGates: []string{GateCIGreen}}, Evidence{PRURL: "u"}, false, "has not finished"},
		{"ci none", fakeSC{pr: PRState{State: "OPEN"}}, Policy{QualityGates: []string{GateCIGreen}}, Evidence{PRURL: "u"}, false, "no CI checks"},
		{"ci green", fakeSC{pr: merged}, Policy{QualityGates: []string{GateCIGreen}}, Evidence{PRURL: "u"}, true, ""},
		{"pasted ok", nil, Policy{QualityGates: []string{GateCIGreen}}, Evidence{GateMode: ModeAuthorPasted, GateOutput: "ok"}, true, ""},
		{"pasted empty", nil, Policy{QualityGates: []string{GateCIGreen}}, Evidence{GateMode: ModeAuthorPasted}, false, "gate_output is empty"},
		{"pasted not opted in", nil, Policy{QualityGates: []string{GateCIGreen}}, Evidence{GateOutput: "ok"}, false, "no PR recorded"},
	}
	for _, c := range cases {
		v := Evaluate(context.Background(), c.sc, c.p, c.ev)
		if v.OK != c.ok {
			t.Errorf("%s: OK=%v missing=%v", c.name, v.OK, v.Missing)
		}
		if c.want != "" && !strings.Contains(strings.Join(v.Missing, ";"), c.want) {
			t.Errorf("%s: missing %v lacks %q", c.name, v.Missing, c.want)
		}
	}
}

func TestParsePRView(t *testing.T) {
	st, err := parsePRView([]byte(`{"url":"u","state":"MERGED","statusCheckRollup":[{"name":"b","conclusion":"SUCCESS"},{"context":"ci/x","state":"FAILURE"},{"context":"ci/y","state":"PENDING"}]}`))
	if err != nil || st.State != "MERGED" || len(st.Checks) != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	if st.Checks[1].Name != "ci/x" || st.Checks[1].Conclusion != "FAILURE" || st.Checks[2].Conclusion != "" {
		t.Fatalf("%+v", st.Checks)
	}
}

func TestParseEvidence(t *testing.T) {
	ev := ParseEvidence(`{"landing":{"pr_url":"u","gate_mode":"author_pasted"}}`)
	if ev.PRURL != "u" || ev.GateMode != ModeAuthorPasted {
		t.Fatalf("%+v", ev)
	}
	if ParseEvidence("not json") != (Evidence{}) {
		t.Fatal("malformed should be empty")
	}
}

func TestCheckClean(t *testing.T) {
	ctx := context.Background()
	if got := CheckClean(ctx, ""); got != nil {
		t.Fatalf("blank dir: %v", got)
	}
	if got := CheckClean(ctx, "/nonexistent/path"); got != nil {
		t.Fatalf("missing dir: %v", got)
	}
	plain := t.TempDir()
	if got := CheckClean(ctx, plain); got != nil {
		t.Fatalf("non-git dir: %v", got)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got := CheckClean(ctx, repo); got != nil {
		t.Fatalf("clean repo: %v", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CheckClean(ctx, repo)
	if len(got) != 1 || !strings.Contains(got[0], "1 uncommitted change") {
		t.Fatalf("dirty repo: %v", got)
	}
}

// Undecided separates "could not tell yet" from "plainly unmet": it is set only
// when every missing requirement is of the first kind.
func TestEvaluateUndecided(t *testing.T) {
	ctxb := context.Background()
	ci := Policy{QualityGates: []string{GateCIGreen}}
	cases := []struct {
		name string
		sc   SourceControl
		p    Policy
		ev   Evidence
		want bool
	}{
		{"pr unreadable", fakeSC{err: errors.New("boom")}, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, true},
		{"no adapter", nil, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, true},
		{"ci unfinished", fakeSC{pr: PRState{State: "OPEN", Checks: []Check{{"build", ""}}}}, ci, Evidence{PRURL: "u"}, true},
		{"no ci checks yet", fakeSC{pr: PRState{State: "OPEN"}}, ci, Evidence{PRURL: "u"}, true},
		{"no pr recorded", fakeSC{}, Policy{OnDoneMerge: "pr"}, Evidence{}, false},
		{"pr open", fakeSC{pr: PRState{State: "OPEN"}}, Policy{OnDoneMerge: "pr"}, Evidence{PRURL: "u"}, false},
		{"ci red", fakeSC{pr: PRState{State: "MERGED", Checks: []Check{{"build", "FAILURE"}}}}, ci, Evidence{PRURL: "u"}, false},
		{"unfinished and red", fakeSC{pr: PRState{State: "MERGED", Checks: []Check{{"a", ""}, {"b", "FAILURE"}}}}, ci, Evidence{PRURL: "u"}, false},
		{"unfinished but pr open", fakeSC{pr: PRState{State: "OPEN", Checks: []Check{{"a", ""}}}}, Policy{OnDoneMerge: "pr", QualityGates: []string{GateCIGreen}}, Evidence{PRURL: "u"}, false},
	}
	for _, c := range cases {
		v := Evaluate(ctxb, c.sc, c.p, c.ev)
		if v.OK {
			t.Errorf("%s: unexpectedly OK", c.name)
		}
		if v.Undecided != c.want {
			t.Errorf("%s: Undecided=%v, want %v (missing %v)", c.name, v.Undecided, c.want, v.Missing)
		}
	}
	if v := Evaluate(ctxb, nil, Policy{OnDoneMerge: "none"}, Evidence{}); v.Undecided {
		t.Error("a satisfied gate is not undecided")
	}
}
