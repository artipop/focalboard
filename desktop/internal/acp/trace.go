package acp

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Trace direction labels. They read as "who told whom", which is the first
// thing you want to know when a message did not arrive.
const (
	TraceFromCLI = "cli→app" // a raw NDJSON line the agent CLI printed
	TraceToCLI   = "app→cli" // a raw NDJSON line written to the agent CLI
	TraceToUI    = "app→ui"  // an event emitted onto the Wails bus
	TraceFromUI  = "ui→app"  // a call the console made into the manager
	TraceApp     = "app"     // a decision taken in between
)

// Tracer appends everything crossing the ACP boundary to a JSONL file. It
// exists because the interesting failures are about a message that never
// arrived — a question that did not reach the console, an answer that did not
// reach the agent — and those are invisible in ordinary logs, which record
// decisions rather than traffic.
//
// A nil Tracer is usable and does nothing, so call sites need no guard.
type Tracer struct {
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	log  *slog.Logger
	path string
	// broken stops a failing file from producing a warning per line.
	broken bool
}

// newTracer opens the trace file when tracing is on. Tracing is enabled by the
// acp config (`debugLog`) or by FOCALBOARD_ACP_DEBUG in the environment, which
// is the one that can be flipped without editing a file the app is reading.
func newTracer(cfg Config, log *slog.Logger) *Tracer {
	if !cfg.DebugLog && os.Getenv("FOCALBOARD_ACP_DEBUG") == "" {
		return nil
	}
	path := cfg.DebugLogPath
	if path == "" {
		path = filepath.Join(filepath.Dir(cfg.WorktreeDir), "acp-debug.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		log.Warn("acp: cannot create the trace directory, tracing off", "path", path, "err", err)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Warn("acp: cannot open the trace file, tracing off", "path", path, "err", err)
		return nil
	}
	t := &Tracer{f: f, enc: json.NewEncoder(f), log: log, path: path}
	log.Info("acp: tracing every ACP message", "file", path)
	t.Event("", TraceApp, "trace_started", map[string]any{"pid": os.Getpid()})
	return t
}

// Path is where the trace is being written, or "" when tracing is off.
func (t *Tracer) Path() string {
	if t == nil {
		return ""
	}
	return t.path
}

// Enabled reports whether anything is being recorded.
func (t *Tracer) Enabled() bool { return t != nil }

// Event records one message. payload is written as-is when it is valid JSON
// and as a string otherwise, so a raw protocol line stays readable.
func (t *Tracer) Event(sessionID, direction, kind string, payload any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken {
		return
	}
	if err := t.enc.Encode(map[string]any{
		"at":      time.Now().Format(time.RFC3339Nano),
		"session": sessionID,
		"dir":     direction,
		"kind":    kind,
		"payload": payload,
	}); err != nil {
		t.broken = true
		t.log.Warn("acp: tracing stopped after a write error", "err", err)
	}
}

// Line records a raw protocol line, keeping it as JSON when it parses so the
// trace can be filtered with jq rather than read as escaped text.
func (t *Tracer) Line(sessionID, direction string, line []byte) {
	if t == nil {
		return
	}
	var parsed any
	if err := json.Unmarshal(line, &parsed); err != nil {
		t.Event(sessionID, direction, "raw", string(line))
		return
	}
	kind := "raw"
	if obj, ok := parsed.(map[string]any); ok {
		// A JSON-RPC message is named by its method; a response carries none
		// and is left as a raw line, which is enough to pair it with its id.
		for _, key := range []string{"method", "type"} {
			if s, ok := obj[key].(string); ok {
				kind = s
				break
			}
		}
	}
	t.Event(sessionID, direction, kind, parsed)
}

func (t *Tracer) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f != nil {
		_ = t.f.Close()
		t.f = nil
	}
}

// tracingEmitter forwards UI events and records them, so the trace shows what
// the console was actually told rather than what we meant to tell it.
type tracingEmitter struct {
	inner UIEmitter
	tr    *Tracer
}

func (e *tracingEmitter) Emit(event string, payload any) {
	sessionID := ""
	if m, ok := payload.(map[string]any); ok {
		if s, ok := m["sessionId"].(string); ok {
			sessionID = s
		}
	}
	e.tr.Event(sessionID, TraceToUI, event, payload)
	e.inner.Emit(event, payload)
}
