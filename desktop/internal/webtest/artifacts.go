package webtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Verdicts a run can end with. The session reads them back from result.json and
// moves the card accordingly, so they are a closed set rather than prose.
const (
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictBlocked = "blocked" // could not be tested at all (preview down, no access)
)

// Result is what report_result writes and the session reads back.
type Result struct {
	Verdict     string    `json:"verdict"`
	Summary     string    `json:"summary"`
	Steps       []string  `json:"steps,omitempty"`
	Bugs        []string  `json:"bugs,omitempty"`
	URL         string    `json:"url,omitempty"`
	Screenshots []string  `json:"screenshots,omitempty"` // paths relative to the artifacts dir
	At          time.Time `json:"at"`
}

// Passed reports whether the card may move on.
func (r Result) Passed() bool { return r.Verdict == VerdictPass }

// NormalizeVerdict maps what a model is likely to write onto the closed set.
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

// Artifacts is the run's output directory. An empty Dir means "nothing is
// persisted": the tools still work, the evidence just does not survive.
type Artifacts struct {
	Dir string

	mu sync.Mutex
	n  int
}

func NewArtifacts(dir string) *Artifacts { return &Artifacts{Dir: dir} }

var unsafeName = regexp.MustCompile(`[^\w.-]+`)

// SaveScreenshot writes a PNG under screenshots/ and returns its path relative
// to the artifacts directory. Names are numbered in call order, so the files
// read as a timeline even when the model names two steps the same.
func (a *Artifacts) SaveScreenshot(name string, png []byte) (string, error) {
	if a.Dir == "" {
		return "", nil
	}
	a.mu.Lock()
	a.n++
	seq := a.n
	a.mu.Unlock()

	clean := strings.Trim(unsafeName.ReplaceAllString(strings.TrimSpace(name), "-"), "-")
	if clean == "" {
		clean = "step"
	}
	clean = strings.TrimSuffix(clean, ".png")
	rel := filepath.Join(ScreenshotDir, fmt.Sprintf("%02d-%s.png", seq, clean))

	full := filepath.Join(a.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("не удалось создать каталог для скриншотов: %w", err)
	}
	if err := os.WriteFile(full, png, 0o644); err != nil {
		return "", fmt.Errorf("не удалось сохранить скриншот: %w", err)
	}
	return rel, nil
}

// WriteResult stores the verdict, listing whatever screenshots were taken so
// the report does not depend on the model remembering them.
func (a *Artifacts) WriteResult(res Result) error {
	if a.Dir == "" {
		return nil
	}
	if len(res.Screenshots) == 0 {
		shots, err := ListScreenshots(a.Dir)
		if err != nil {
			return err
		}
		res.Screenshots = shots
	}
	if res.At.IsZero() {
		res.At = time.Now()
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.Dir, ResultFile), append(out, '\n'), 0o644)
}

// ReadResult loads the verdict a run left behind. A missing file is reported as
// os.ErrNotExist so the caller can tell "the agent never reported" from "the
// report is broken".
func ReadResult(dir string) (Result, error) {
	b, err := os.ReadFile(filepath.Join(dir, ResultFile))
	if err != nil {
		return Result{}, err
	}
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return Result{}, fmt.Errorf("не удалось разобрать %s: %w", ResultFile, err)
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
