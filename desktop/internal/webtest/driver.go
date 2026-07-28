package webtest

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Driver is the browser seam. mcp.go only ever talks through this interface, so
// the tools are exercised in tests without spawning Chrome — the same trick
// dokku.Runner plays with ssh.
type Driver interface {
	// Navigate opens an already-validated absolute URL.
	Navigate(ctx context.Context, url string) error
	// URL is where the page currently is.
	URL(ctx context.Context) (string, error)
	// Snapshot returns the page outline with element refs (see snapshot.go).
	Snapshot(ctx context.Context) (string, error)

	Click(ctx context.Context, ref string) error
	Fill(ctx context.Context, ref, text string) error
	SelectOption(ctx context.Context, ref, value string) error
	Hover(ctx context.Context, ref string) error
	Press(ctx context.Context, key string) error

	// WaitText waits for text to appear anywhere on the page.
	WaitText(ctx context.Context, text string, timeout time.Duration) error
	// WaitRef waits for a ref from the last snapshot to exist.
	WaitRef(ctx context.Context, ref string, timeout time.Duration) error
	// WaitIdle waits for the network to go quiet.
	WaitIdle(ctx context.Context, timeout time.Duration) error

	// Screenshot returns a PNG of the viewport, or of the whole page.
	Screenshot(ctx context.Context, fullPage bool) ([]byte, error)
	// Eval runs an expression in the page and returns its value as text.
	Eval(ctx context.Context, expression string) (string, error)

	// ConsoleLog and NetworkLog return everything recorded since the run began.
	ConsoleLog() []ConsoleEntry
	NetworkLog() []NetworkEntry

	Close() error
}

// ConsoleEntry is one console message or uncaught page error.
type ConsoleEntry struct {
	At    time.Time
	Level string // log, warn, error, exception
	Text  string
}

func (e ConsoleEntry) String() string {
	return fmt.Sprintf("[%s] %s: %s", e.At.Format("15:04:05"), e.Level, strings.TrimSpace(e.Text))
}

// NetworkEntry is one request worth reporting: a failure, or a response the
// server refused. Successful traffic is not recorded — it only buries the bug.
type NetworkEntry struct {
	At     time.Time
	Method string
	URL    string
	Status int    // 0 when the request never completed
	Error  string // transport-level failure, if any
}

func (e NetworkEntry) String() string {
	switch {
	case e.Status > 0:
		return fmt.Sprintf("[%s] %d %s %s", e.At.Format("15:04:05"), e.Status, e.Method, e.URL)
	default:
		return fmt.Sprintf("[%s] FAILED %s %s (%s)", e.At.Format("15:04:05"), e.Method, e.URL, e.Error)
	}
}

// interesting reports whether a response is worth keeping in the log.
func interestingStatus(status int) bool { return status >= 400 }
