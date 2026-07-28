package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub answers the questions the repository alone cannot: is there a pull
// request for this branch, was it merged, did anyone approve it, did the checks
// pass. It talks to the REST API directly — no gh CLI, nothing to install.
//
// A token is optional. Public repositories work without one at 60 requests an
// hour per address, which is why an unauthenticated watcher polls far less
// often; private repositories need a token to answer at all.
type GitHub struct {
	Token   string
	BaseURL string // defaults to https://api.github.com; tests point it elsewhere
	Remote  string // remote whose URL names the repository
	HTTP    *http.Client
	Run     Runner // git, for reading the remote URL

	// MinInterval bounds how often one target is polled. Zero picks a default
	// from whether a token is set.
	MinInterval time.Duration

	mu     sync.Mutex
	lastAt map[string]time.Time
}

func (g *GitHub) Name() string { return "github" }

// githubTriggers are the events only the API can answer.
var githubTriggers = []string{
	KindPROpened, KindPRMerged, KindPRClosed,
	KindReviewApproved, KindChecksPassed, KindChecksFailed,
}

func (g *GitHub) Poll(ctx context.Context, t Target) ([]Event, error) {
	wanted := false
	for _, k := range githubTriggers {
		if t.Wants(k) {
			wanted = true
			break
		}
	}
	if !wanted || t.RepoPath == "" || t.Branch == "" {
		return nil, nil
	}
	if !g.due(t) {
		return nil, nil
	}

	owner, repo, err := g.repository(ctx, t)
	if err != nil || owner == "" {
		return nil, err
	}

	pr, err := g.latestPR(ctx, owner, repo, t.Branch)
	if err != nil {
		return nil, err
	}
	if pr == nil {
		return nil, nil
	}

	var events []Event
	number := strconv.Itoa(pr.Number)
	add := func(kind, detail, marker string) {
		if !t.Wants(kind) {
			return
		}
		events = append(events, Event{
			Kind: kind, RepoPath: t.RepoPath, Branch: t.Branch, Detail: detail,
			URL: pr.HTMLURL, Number: pr.Number, Marker: marker, At: time.Now(),
		})
	}
	switch {
	case pr.MergedAt != "":
		add(KindPRMerged, fmt.Sprintf("PR #%d смержен", pr.Number), number)
	case pr.State == "closed":
		add(KindPRClosed, fmt.Sprintf("PR #%d закрыт без мержа", pr.Number), number)
	default:
		add(KindPROpened, fmt.Sprintf("открыт PR #%d", pr.Number), number)
	}

	if t.Wants(KindReviewApproved) {
		approved, err := g.approved(ctx, owner, repo, pr.Number)
		if err != nil {
			return events, err
		}
		if approved != "" {
			add(KindReviewApproved, fmt.Sprintf("PR #%d одобрен на ревью", pr.Number), approved)
		}
	}
	if t.Wants(KindChecksPassed) || t.Wants(KindChecksFailed) {
		state, err := g.checks(ctx, owner, repo, pr.Head.SHA)
		if err != nil {
			return events, err
		}
		switch state {
		case KindChecksPassed:
			add(KindChecksPassed, fmt.Sprintf("проверки PR #%d прошли", pr.Number), pr.Head.SHA)
		case KindChecksFailed:
			add(KindChecksFailed, fmt.Sprintf("проверки PR #%d упали", pr.Number), pr.Head.SHA)
		}
	}
	return events, nil
}

// due rate-limits one target. Unauthenticated calls share a 60-per-hour budget
// with everything else on the machine, so they are spread much further apart.
func (g *GitHub) due(t Target) bool {
	interval := g.MinInterval
	if interval == 0 {
		interval = 5 * time.Minute
		if g.Token != "" {
			interval = time.Minute
		}
	}
	key := t.RepoPath + "\x00" + t.Branch

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lastAt == nil {
		g.lastAt = map[string]time.Time{}
	}
	if last, ok := g.lastAt[key]; ok && time.Since(last) < interval {
		return false
	}
	g.lastAt[key] = time.Now()
	return true
}

// repository resolves owner/repo from the remote URL. A remote that is not on
// github.com yields nothing at all rather than an error: the watcher simply has
// nothing to say about it.
func (g *GitHub) repository(ctx context.Context, t Target) (owner, repo string, err error) {
	remote := t.RemoteOr(g.Remote)
	run := g.Run
	if run == nil {
		run = Exec
	}
	out, err := run(ctx, t.RepoPath, "remote", "get-url", remote)
	if err != nil {
		return "", "", fmt.Errorf("не удалось прочитать адрес remote %s: %w", remote, err)
	}
	owner, repo = ParseGitHubRemote(strings.TrimSpace(out))
	return owner, repo, nil
}

// ParseGitHubRemote extracts owner and repository from the forms git prints:
// git@github.com:owner/repo.git, https://github.com/owner/repo.git,
// ssh://git@github.com/owner/repo. Anything else yields empty strings.
func ParseGitHubRemote(raw string) (owner, repo string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	var path string
	switch {
	case strings.HasPrefix(raw, "git@"):
		at := strings.Index(raw, "@")
		colon := strings.Index(raw, ":")
		if colon < 0 || !strings.EqualFold(raw[at+1:colon], "github.com") {
			return "", ""
		}
		path = raw[colon+1:]
	default:
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Host, "github.com") {
			return "", ""
		}
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}

type githubPR struct {
	Number   int    `json:"number"`
	State    string `json:"state"`
	MergedAt string `json:"merged_at"`
	HTMLURL  string `json:"html_url"`
	Head     struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	UpdatedAt string `json:"updated_at"`
}

// latestPR is the most recently updated pull request for the branch, open or
// not: a card waits for its own PR to land, and a landed one is closed.
func (g *GitHub) latestPR(ctx context.Context, owner, repo, branch string) (*githubPR, error) {
	q := url.Values{}
	q.Set("head", owner+":"+branch)
	q.Set("state", "all")
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("per_page", "5")

	var prs []githubPR
	if err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/pulls?%s", owner, repo, q.Encode()), &prs); err != nil {
		return nil, err
	}
	for i := range prs {
		if strings.EqualFold(prs[i].Head.Ref, branch) {
			return &prs[i], nil
		}
	}
	return nil, nil
}

// approved returns a marker identifying the latest approving review, or "".
func (g *GitHub) approved(ctx context.Context, owner, repo string, number int) (string, error) {
	var reviews []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	if err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repo, number), &reviews); err != nil {
		return "", err
	}
	marker := ""
	for _, r := range reviews {
		if strings.EqualFold(r.State, "APPROVED") {
			marker = strconv.FormatInt(r.ID, 10)
		}
	}
	return marker, nil
}

// checks folds the check runs of a commit into one answer. Anything still
// running means no answer yet — a half-finished pipeline is not a verdict.
func (g *GitHub) checks(ctx context.Context, owner, repo, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	var payload struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", owner, repo, sha), &payload); err != nil {
		return "", err
	}
	if payload.TotalCount == 0 || len(payload.CheckRuns) == 0 {
		return "", nil
	}
	failed := false
	for _, run := range payload.CheckRuns {
		if !strings.EqualFold(run.Status, "completed") {
			return "", nil
		}
		switch strings.ToLower(run.Conclusion) {
		case "success", "neutral", "skipped":
		default:
			failed = true
		}
	}
	if failed {
		return KindChecksFailed, nil
	}
	return KindChecksPassed, nil
}

func (g *GitHub) get(ctx context.Context, path string, out any) error {
	base := strings.TrimSuffix(g.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// A private repository without a token looks exactly like a missing one.
		if g.Token == "" {
			return fmt.Errorf("GitHub отвечает 404 на %s — для приватного репозитория нужен токен (githubToken в конфиге или GITHUB_TOKEN)", path)
		}
		return fmt.Errorf("GitHub: 404 на %s", path)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if g.Token == "" {
			return fmt.Errorf("GitHub исчерпал лимит запросов без токена (60 в час) — задай githubToken в конфиге или GITHUB_TOKEN")
		}
		return fmt.Errorf("GitHub отклонил запрос %s: %s", path, resp.Status)
	case resp.StatusCode >= 300:
		return fmt.Errorf("GitHub: %s на %s", resp.Status, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
