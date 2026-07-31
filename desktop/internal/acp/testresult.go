package acp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The contract between a test session and the board: the agent drives a browser
// through an MCP server of its own (Playwright, say) and leaves its verdict in
// result.json inside the run's artifacts directory, which is what moves the
// card. Only this half is ours — the browser is not.

// ResultFile is what the agent writes; ScreenshotDir is where it puts the
// evidence, both relative to the run's artifacts directory.
const (
	ResultFile    = "result.json"
	ScreenshotDir = "screenshots"
)

// Verdicts a run can end with. The session reads them back from result.json and
// moves the card accordingly, so they are a closed set rather than prose.
const (
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictBlocked = "blocked" // could not be tested at all (preview down, no access)
)

// TestResult is what the agent reports and the session reads back.
type TestResult struct {
	Verdict     string    `json:"verdict"`
	Summary     string    `json:"summary"`
	Steps       []string  `json:"steps,omitempty"`
	Bugs        []string  `json:"bugs,omitempty"`
	URL         string    `json:"url,omitempty"`
	Screenshots []string  `json:"screenshots,omitempty"` // paths relative to the artifacts dir
	At          time.Time `json:"at"`
}

// Passed reports whether the card may move on.
func (r TestResult) Passed() bool { return r.Verdict == VerdictPass }

// NormalizeVerdict maps what a model is likely to write onto the closed set.
// The file is written by an agent following a prompt rather than by a tool with
// an enum, so the wording has to be met halfway.
func NormalizeVerdict(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pass", "passed", "ok", "success", "успех", "прошёл", "прошел":
		return VerdictPass, nil
	case "fail", "failed", "error", "провал", "не прошёл", "не прошел":
		return VerdictFail, nil
	case "blocked", "skip", "skipped", "заблокировано", "не проверено":
		return VerdictBlocked, nil
	default:
		return "", fmt.Errorf("verdict должен быть pass, fail или blocked, а не %q", v)
	}
}

// ReadTestResult loads the verdict a run left behind. A missing file is reported
// as os.ErrNotExist so the caller can tell "the agent never reported" from "the
// report is broken".
func ReadTestResult(dir string) (TestResult, error) {
	b, err := os.ReadFile(filepath.Join(dir, ResultFile))
	if err != nil {
		return TestResult{}, err
	}
	var res TestResult
	if err := json.Unmarshal(b, &res); err != nil {
		return TestResult{}, fmt.Errorf("не удалось разобрать %s: %w", ResultFile, err)
	}
	verdict, err := NormalizeVerdict(res.Verdict)
	if err != nil {
		return TestResult{}, err
	}
	res.Verdict = verdict
	if len(res.Screenshots) == 0 {
		// The report does not have to remember its own evidence: whatever the
		// agent saved in the directory counts.
		shots, err := ListScreenshots(dir)
		if err != nil {
			return TestResult{}, err
		}
		res.Screenshots = shots
	}
	return res, nil
}

// ListScreenshots returns the run's screenshots, relative to dir, in order.
func ListScreenshots(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, ScreenshotDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		out = append(out, filepath.Join(ScreenshotDir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
