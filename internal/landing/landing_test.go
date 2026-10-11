package landing

import (
	"context"
	"errors"
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
