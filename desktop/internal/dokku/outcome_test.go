package dokku

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutcomeRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, err := ReadOutcome(dir); !os.IsNotExist(err) {
		t.Fatalf("a run that never deployed must be distinguishable: %v", err)
	}

	if err := WriteOutcome(dir, Outcome{OK: true, App: "api-feat-x", Branch: "feat/x", URL: "https://feat-x.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOutcome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.App != "api-feat-x" || got.URL != "https://feat-x.example.com" {
		t.Fatalf("outcome: %+v", got)
	}
	if got.At.IsZero() {
		t.Fatal("the time of the attempt should be filled in")
	}

	// A later attempt replaces the earlier one: what matters is where the
	// branch stands when the session ends.
	if err := WriteOutcome(dir, Outcome{Branch: "feat/x", Error: "build failed"}); err != nil {
		t.Fatal(err)
	}
	got, err = ReadOutcome(dir)
	if err != nil || got.OK || got.Error != "build failed" {
		t.Fatalf("second attempt: %+v, %v", got, err)
	}

	// Nothing to record into is not an error.
	if err := WriteOutcome("", Outcome{OK: true}); err != nil {
		t.Fatalf("empty dir: %v", err)
	}
}

func TestDeployToolRecordsWhatHappened(t *testing.T) {
	dir := t.TempDir()
	cl, _ := New(testTarget(), "/repo", "feat/x")
	cl.Run = (&fakeRunner{}).run
	cs := connectWithArtifacts(t, cl, dir)

	if _, isErr := callText(t, cs, "deploy_branch", nil); isErr {
		t.Fatal("the fake host deploys fine")
	}
	got, err := ReadOutcome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Branch != "feat/x" || got.App == "" {
		t.Fatalf("successful deploy recorded as: %+v", got)
	}

	// A failing push must be recorded as a failure, with the reason.
	cl.Run = (&fakeRunner{replies: map[string]reply{
		"git push": {out: "! [remote rejected] master -> master", err: errors.New("git push failed")},
	}}).run
	if _, isErr := callText(t, cs, "deploy_branch", nil); !isErr {
		t.Fatal("a failed push should be an error result")
	}
	got, err = ReadOutcome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == "" {
		t.Fatalf("failed deploy recorded as: %+v", got)
	}
	if !strings.Contains(strings.ToLower(got.Error), "git") {
		t.Fatalf("the reason should say what broke: %q", got.Error)
	}
}

// recordOutcome must never break a tool call, however unwritable the directory.
func TestRecordOutcomeSurvivesABadDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordOutcome(file, Result{App: "a"}, "feat/x", errors.New("boom"))
}
