package dokku

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A deploy session records what actually happened, so the card's route can move
// on the truth rather than on the agent falling silent: "the model stopped
// talking" is not the same as "the branch is live". The file is written by this
// MCP server and read back by the session that spawned it — the same contract
// webtest's result.json follows.

// EnvArtifacts points the server at the session's artifacts directory. Empty
// means nothing is recorded; the tools still work.
const EnvArtifacts = "FOCALBOARD_DOKKU_ARTIFACTS"

// OutcomeFile is written into that directory after every deploy attempt.
const OutcomeFile = "deploy.json"

// Outcome is the recorded result of the last deploy attempt.
type Outcome struct {
	OK     bool      `json:"ok"`
	App    string    `json:"app"`
	Branch string    `json:"branch"`
	URL    string    `json:"url,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// WriteOutcome records an attempt. A later attempt overwrites an earlier one:
// what matters is where the branch stands when the session ends.
func WriteOutcome(dir string, o Outcome) error {
	if dir == "" {
		return nil
	}
	if o.At.IsZero() {
		o.At = time.Now()
	}
	out, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, OutcomeFile), append(out, '\n'), 0o644)
}

// ReadOutcome loads what a deploy session left behind. A missing file is
// reported as os.ErrNotExist, so "never deployed" is distinguishable from
// "the record is broken".
func ReadOutcome(dir string) (Outcome, error) {
	b, err := os.ReadFile(filepath.Join(dir, OutcomeFile))
	if err != nil {
		return Outcome{}, err
	}
	var o Outcome
	if err := json.Unmarshal(b, &o); err != nil {
		return Outcome{}, fmt.Errorf("не удалось разобрать %s: %w", OutcomeFile, err)
	}
	return o, nil
}
