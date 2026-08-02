package acp

import "io"

// The debug log used to be fed by the claude bridge, which saw every line of
// the CLI's own protocol. With every agent spoken to in ACP directly there is
// no bridge to report from, so the trace is taken off the wire itself: the
// agent's stdin and stdout are teed line by line into the tracer. That makes
// the log say the same thing for every kind — the raw protocol, in order —
// rather than only for the one agent that happened to have a bridge.
//
// Tracing is off by default, and when it is off these wrap nothing at all.

// traceReader tees everything the agent says into the trace. It reassembles
// lines as they pass rather than reading ahead, so the agent's stream reaches
// the SDK exactly as it arrived and a line too long to record still gets
// through.
func (m *Manager) traceReader(sessionID string, r io.Reader) io.Reader {
	if !m.tr.Enabled() {
		return r
	}
	return &tracingReader{sessionID: sessionID, r: r, tr: m.tr}
}

type tracingReader struct {
	sessionID string
	r         io.Reader
	tr        *Tracer
	line      []byte
	dropping  bool // the current line outgrew the limit; record nothing of it
}

func (t *tracingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			if !t.dropping && len(t.line) > 0 {
				t.tr.Line(t.sessionID, TraceFromCLI, t.line)
			}
			t.line, t.dropping = t.line[:0], false
			continue
		}
		if t.dropping {
			continue
		}
		if len(t.line) >= maxTraceLine {
			t.line, t.dropping = t.line[:0], true
			continue
		}
		t.line = append(t.line, b)
	}
	return n, err
}

// traceWriter tees everything we say to the agent into the trace. Writes go to
// the agent first: the trace must never be what delays a turn.
func (m *Manager) traceWriter(sessionID string, w io.Writer) io.Writer {
	if !m.tr.Enabled() {
		return w
	}
	return &tracingWriter{sessionID: sessionID, w: w, tr: m.tr}
}

// maxTraceLine bounds one traced line. A message longer than this is still
// delivered — the tee only stops recording it — because a file the agent read
// into the conversation can be far larger than anything worth logging.
const maxTraceLine = 4 << 20

type tracingWriter struct {
	sessionID string
	w         io.Writer
	tr        *Tracer
}

func (t *tracingWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.tr.Line(t.sessionID, TraceToCLI, trimNewline(p[:n]))
	}
	return n, err
}

// trimNewline drops the delimiter the SDK writes after each message, so the
// traced payload is the message itself.
func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
