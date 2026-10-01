package redact

import "testing"

func TestText_ReplacesExactOccurrences(t *testing.T) {
	got := New("sk-live-123456").Text(`bad token "sk-live-123456" (retry sk-live-123456)`)
	if want := `bad token "[redacted]" (retry [redacted])`; got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
}

// Whole text keeps an ordinary word that merely starts or ends like a
// secret: only exact occurrences go.
func TestText_LeavesPartialMatches(t *testing.T) {
	text := "messaging service not configured"
	if got := New("default", "dsk_abcdef").Text(text); got != text {
		t.Fatalf("Text = %q, want it unchanged", got)
	}
}

func TestText_LongestSecretFirst(t *testing.T) {
	if got := New("abcd", "abcd-efgh-ijkl").Text("key=abcd-efgh-ijkl"); got != "key=[redacted]" {
		t.Fatalf("Text = %q, want the longer secret replaced whole", got)
	}
}

func TestNew_IgnoresShortValues(t *testing.T) {
	if r := New("1", "on", "", "10"); r != nil {
		t.Fatalf("New = %+v, want nil when no value is long enough", r)
	}
	text := "exit 1 on retry 10"
	if got := New("1", "on", "token-one").Text(text); got != text {
		t.Fatalf("Text = %q, want short values ignored", got)
	}
}

func TestRedactor_NilRedactsNothing(t *testing.T) {
	var r *Redactor
	if got := r.Text("whatever-secret"); got != "whatever-secret" {
		t.Fatalf("nil Redactor redacted: %q", got)
	}
}
