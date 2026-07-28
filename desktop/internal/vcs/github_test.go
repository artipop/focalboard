package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// githubFixture serves canned API responses and records what was asked for.
type githubFixture struct {
	pulls    string
	reviews  string
	checks   string
	requests []string
	auth     string
	status   int
}

func (f *githubFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
		f.auth = r.Header.Get("Authorization")
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			_, _ = w.Write([]byte(orEmpty(f.reviews, "[]")))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(orEmpty(f.checks, `{"total_count":0,"check_runs":[]}`)))
		default:
			_, _ = w.Write([]byte(orEmpty(f.pulls, "[]")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func orEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// watcher wires a GitHub watcher to the fixture, with git faked out to a fixed
// remote URL so no repository is needed.
func (f *githubFixture) watcher(t *testing.T, token string) *GitHub {
	t.Helper()
	return &GitHub{
		Token:       token,
		BaseURL:     f.server(t).URL,
		MinInterval: time.Nanosecond,
		Run: func(_ context.Context, _ string, args ...string) (string, error) {
			return "git@github.com:acme/webapp.git", nil
		},
	}
}

func prJSON(state, mergedAt string) string {
	pr := map[string]any{
		"number": 42, "state": state, "html_url": "https://github.com/acme/webapp/pull/42",
		"head": map[string]any{"sha": "abc123", "ref": "feat/x"},
	}
	if mergedAt != "" {
		pr["merged_at"] = mergedAt
	}
	b, _ := json.Marshal([]any{pr})
	return string(b)
}

func target(kinds ...string) Target {
	return Target{RepoPath: "/repo", Branch: "feat/x", Triggers: kinds}
}

func TestGitHubReportsPullRequestState(t *testing.T) {
	cases := []struct {
		name, state, mergedAt, want string
	}{
		{"открыт", "open", "", KindPROpened},
		{"смержен", "closed", "2026-07-01T10:00:00Z", KindPRMerged},
		{"закрыт без мержа", "closed", "", KindPRClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &githubFixture{pulls: prJSON(c.state, c.mergedAt)}
			w := f.watcher(t, "tok")

			events, err := w.Poll(context.Background(), target(KindPROpened, KindPRMerged, KindPRClosed))
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Kind != c.want {
				t.Fatalf("events: %+v", events)
			}
			if events[0].Number != 42 || events[0].URL == "" || events[0].Marker != "42" {
				t.Fatalf("event: %+v", events[0])
			}
			if f.auth != "Bearer tok" {
				t.Fatalf("token not sent: %q", f.auth)
			}
		})
	}
}

func TestGitHubOnlyAsksForWhatIsWaitedOn(t *testing.T) {
	f := &githubFixture{pulls: prJSON("open", "")}
	w := f.watcher(t, "tok")

	// Nothing GitHub can answer: not a single request.
	if _, err := w.Poll(context.Background(), target(KindBranchMerged)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("requests: %v", f.requests)
	}

	// Waiting for a pull request costs exactly one request; reviews and checks
	// are only fetched when they are waited on.
	if _, err := w.Poll(context.Background(), target(KindPROpened)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || !strings.Contains(f.requests[0], "/pulls?") {
		t.Fatalf("requests: %v", f.requests)
	}
	if !strings.Contains(f.requests[0], "head=acme%3Afeat%2Fx") {
		t.Fatalf("the query should scope to the branch: %v", f.requests)
	}
}

func TestGitHubReviewsAndChecks(t *testing.T) {
	f := &githubFixture{
		pulls:   prJSON("open", ""),
		reviews: `[{"id":1,"state":"COMMENTED"},{"id":2,"state":"APPROVED"}]`,
		checks:  `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"skipped"}]}`,
	}
	w := f.watcher(t, "tok")

	events, err := w.Poll(context.Background(), target(KindReviewApproved, KindChecksPassed, KindChecksFailed))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, e := range events {
		kinds[e.Kind] = e.Marker
	}
	if kinds[KindReviewApproved] != "2" {
		t.Fatalf("approval marker should follow the review: %+v", events)
	}
	if kinds[KindChecksPassed] != "abc123" {
		t.Fatalf("checks marker should follow the commit: %+v", events)
	}
	if _, ok := kinds[KindPROpened]; ok {
		t.Fatalf("pr.opened was not waited on: %+v", events)
	}

	// A failing run is a failure; a run still going is no answer at all.
	f.checks = `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"failure"}]}`
	events, _ = w.Poll(context.Background(), target(KindChecksPassed, KindChecksFailed))
	if len(events) != 1 || events[0].Kind != KindChecksFailed {
		t.Fatalf("events: %+v", events)
	}
	f.checks = `{"total_count":2,"check_runs":[{"status":"in_progress"},{"status":"completed","conclusion":"success"}]}`
	events, _ = w.Poll(context.Background(), target(KindChecksPassed, KindChecksFailed))
	if len(events) != 0 {
		t.Fatalf("an unfinished pipeline is not a verdict: %+v", events)
	}
}

func TestGitHubWithoutAPullRequestSaysNothing(t *testing.T) {
	f := &githubFixture{pulls: "[]"}
	w := f.watcher(t, "")

	events, err := w.Poll(context.Background(), target(KindPRMerged))
	if err != nil || len(events) != 0 {
		t.Fatalf("events: %+v, %v", events, err)
	}
}

func TestGitHubRateLimitPointsAtTheToken(t *testing.T) {
	f := &githubFixture{status: http.StatusForbidden}
	w := f.watcher(t, "")

	_, err := w.Poll(context.Background(), target(KindPRMerged))
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("an unauthenticated rate limit should name the fix: %v", err)
	}
}

func TestGitHubPacesItself(t *testing.T) {
	f := &githubFixture{pulls: prJSON("open", "")}
	w := f.watcher(t, "tok")
	w.MinInterval = time.Hour

	if _, err := w.Poll(context.Background(), target(KindPROpened)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Poll(context.Background(), target(KindPROpened)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("the second poll came too soon and should have been skipped: %v", f.requests)
	}
}

func TestParseGitHubRemote(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:acme/webapp.git":       {"acme", "webapp"},
		"https://github.com/acme/webapp.git":   {"acme", "webapp"},
		"https://github.com/acme/webapp":       {"acme", "webapp"},
		"ssh://git@github.com/acme/webapp.git": {"acme", "webapp"},
		"git@gitlab.com:acme/webapp.git":       {"", ""},
		"https://git.example.com/acme/web.git": {"", ""},
		"/local/path/without/a/remote":         {"", ""},
		"https://github.com/acme":              {"", ""},
	}
	for raw, want := range cases {
		owner, repo := ParseGitHubRemote(raw)
		if owner != want[0] || repo != want[1] {
			t.Errorf("ParseGitHubRemote(%q) = %q/%q, want %q/%q", raw, owner, repo, want[0], want[1])
		}
	}
}
