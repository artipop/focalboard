package webtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeDriver is a browser that only remembers what it was told to do, so the
// tools are exercised through the real protocol without spawning Chrome.
type fakeDriver struct {
	url      string
	snapshot string
	actions  []string
	png      []byte
	console  []ConsoleEntry
	network  []NetworkEntry
	fail     error
}

var _ Driver = (*fakeDriver)(nil)

func (d *fakeDriver) record(format string, args ...any) error {
	d.actions = append(d.actions, fmt.Sprintf(format, args...))
	return d.fail
}

func (d *fakeDriver) Navigate(_ context.Context, u string) error {
	if d.fail != nil {
		return d.fail
	}
	d.url = u
	return d.record("navigate %s", u)
}
func (d *fakeDriver) URL(context.Context) (string, error) { return d.url, nil }
func (d *fakeDriver) Snapshot(context.Context) (string, error) {
	if d.fail != nil {
		return "", d.fail
	}
	return d.snapshot, nil
}
func (d *fakeDriver) Click(_ context.Context, ref string) error { return d.record("click %s", ref) }
func (d *fakeDriver) Fill(_ context.Context, ref, text string) error {
	return d.record("fill %s=%s", ref, text)
}
func (d *fakeDriver) SelectOption(_ context.Context, ref, v string) error {
	return d.record("select %s=%s", ref, v)
}
func (d *fakeDriver) Hover(_ context.Context, ref string) error { return d.record("hover %s", ref) }
func (d *fakeDriver) Press(_ context.Context, key string) error { return d.record("press %s", key) }
func (d *fakeDriver) WaitText(_ context.Context, text string, _ time.Duration) error {
	return d.record("waitText %s", text)
}
func (d *fakeDriver) WaitRef(_ context.Context, ref string, _ time.Duration) error {
	return d.record("waitRef %s", ref)
}
func (d *fakeDriver) WaitIdle(context.Context, time.Duration) error { return d.record("waitIdle") }
func (d *fakeDriver) Screenshot(context.Context, bool) ([]byte, error) {
	if d.fail != nil {
		return nil, d.fail
	}
	return d.png, nil
}
func (d *fakeDriver) Eval(_ context.Context, expr string) (string, error) {
	return "42", d.record("eval %s", expr)
}
func (d *fakeDriver) ConsoleLog() []ConsoleEntry { return d.console }
func (d *fakeDriver) NetworkLog() []NetworkEntry { return d.network }
func (d *fakeDriver) Close() error               { return nil }

func testConfig(t *testing.T, dir string) Config {
	t.Helper()
	base, err := url.Parse("https://feature-x.preview.example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		BaseURL:   base,
		Artifacts: dir,
		Secrets:   map[string]string{"password": "hunter2"},
	}
}

// connect wires the server to an in-memory client, so the tools are called over
// the real protocol.
func connect(t *testing.T, cfg Config, drv Driver, art *Artifacts) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := NewServer(cfg, drv, art).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return res, b.String()
}

func TestToolsList(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, testConfig(t, dir), &fakeDriver{}, NewArtifacts(dir))

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"click", "console_log", "eval_js", "fill", "fill_secret", "hover", "network_log",
		"open_page", "press_key", "report_result", "screenshot", "select_option", "snapshot", "wait_for",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools: %v", names)
	}
}

func TestOpenPageResolvesPathsAndRefusesOtherHosts(t *testing.T) {
	dir := t.TempDir()
	drv := &fakeDriver{snapshot: "url: x\ntitle: y\n\nbutton \"Купить\" [e1]"}
	cs := connect(t, testConfig(t, dir), drv, NewArtifacts(dir))

	if _, text := call(t, cs, "open_page", map[string]any{"path": "/checkout"}); !strings.Contains(text, "https://feature-x.preview.example.com/checkout") {
		t.Fatalf("relative path not resolved against the preview: %s", text)
	}
	if !strings.Contains(drv.actions[0], "/checkout") {
		t.Fatalf("driver actions: %v", drv.actions)
	}
	// The snapshot rides along, so the model does not need a second call.
	if _, text := call(t, cs, "open_page", nil); !strings.Contains(text, `[e1]`) {
		t.Fatalf("open_page did not return the page outline: %s", text)
	}

	res, text := call(t, cs, "open_page", map[string]any{"path": "https://evil.example.com/"})
	if !res.IsError || !strings.Contains(text, "только страницы") {
		t.Fatalf("a foreign host must be refused, got: %s", text)
	}
	for _, a := range drv.actions {
		if strings.Contains(a, "evil.example.com") {
			t.Fatalf("the browser was pointed at a foreign host: %v", drv.actions)
		}
	}
}

func TestFillSecretNeverEchoesTheValue(t *testing.T) {
	dir := t.TempDir()
	drv := &fakeDriver{}
	cs := connect(t, testConfig(t, dir), drv, NewArtifacts(dir))

	_, text := call(t, cs, "fill_secret", map[string]any{"ref": "e3", "name": "password"})
	if strings.Contains(text, "hunter2") {
		t.Fatalf("the secret leaked into the tool result: %s", text)
	}
	if drv.actions[0] != "fill e3=hunter2" {
		t.Fatalf("the secret did not reach the page: %v", drv.actions)
	}

	res, text := call(t, cs, "fill_secret", map[string]any{"ref": "e3", "name": "nope"})
	if !res.IsError || !strings.Contains(text, "password") {
		t.Fatalf("an unknown secret should list what exists, got: %s", text)
	}
}

func TestScreenshotIsSavedAndReturnedAsImage(t *testing.T) {
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	cs := connect(t, testConfig(t, dir), &fakeDriver{png: png}, NewArtifacts(dir))

	res, text := call(t, cs, "screenshot", map[string]any{"name": "шаг 1: оформление"})
	var images int
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			images++
		}
	}
	if images != 1 {
		t.Fatalf("expected the png back as an image, content: %+v", res.Content)
	}
	shots, err := ListScreenshots(dir)
	if err != nil || len(shots) != 1 {
		t.Fatalf("screenshots: %v, %v", shots, err)
	}
	if !strings.Contains(text, shots[0]) {
		t.Fatalf("the path should be in the text too: %s", text)
	}
	if data, err := os.ReadFile(filepath.Join(dir, shots[0])); err != nil || string(data) != string(png) {
		t.Fatalf("file contents: %v, %v", string(data), err)
	}
}

func TestReportResultWritesTheVerdict(t *testing.T) {
	dir := t.TempDir()
	drv := &fakeDriver{url: "https://feature-x.preview.example.com/checkout", png: []byte("png")}
	art := NewArtifacts(dir)
	cs := connect(t, testConfig(t, dir), drv, art)
	call(t, cs, "screenshot", map[string]any{"name": "checkout"})

	// A verdict that says "broken" without saying what is broken is refused.
	if res, _ := call(t, cs, "report_result", map[string]any{"verdict": "fail", "summary": "всё плохо"}); !res.IsError {
		t.Fatal("fail without bugs must be rejected")
	}
	if res, _ := call(t, cs, "report_result", map[string]any{"verdict": "maybe", "summary": "s"}); !res.IsError {
		t.Fatal("an unknown verdict must be rejected")
	}

	call(t, cs, "report_result", map[string]any{
		"verdict": "прошёл",
		"summary": "Оформление заказа работает",
		"steps":   []any{"открыл каталог", "оформил заказ"},
	})
	res, err := ReadResult(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed() {
		t.Fatalf("verdict not normalized: %+v", res)
	}
	if res.URL != drv.url || len(res.Steps) != 2 {
		t.Fatalf("result: %+v", res)
	}
	// Screenshots are listed from disk, not from the model's memory.
	if len(res.Screenshots) != 1 {
		t.Fatalf("screenshots not recorded: %+v", res.Screenshots)
	}
}

func TestLogsReportWhatBroke(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	drv := &fakeDriver{
		console: []ConsoleEntry{{At: now, Level: "error", Text: "TypeError: undefined is not a function"}},
		network: []NetworkEntry{{At: now, Method: "GET", URL: "https://x/api/cart", Status: 500}},
	}
	cs := connect(t, testConfig(t, dir), drv, NewArtifacts(dir))

	if _, text := call(t, cs, "console_log", nil); !strings.Contains(text, "TypeError") {
		t.Fatalf("console: %s", text)
	}
	if _, text := call(t, cs, "network_log", nil); !strings.Contains(text, "500") {
		t.Fatalf("network: %s", text)
	}

	empty := connect(t, testConfig(t, dir), &fakeDriver{}, NewArtifacts(dir))
	if _, text := call(t, empty, "console_log", nil); !strings.Contains(text, "пуста") {
		t.Fatalf("an empty console should say so: %s", text)
	}
}

func TestWaitForNeedsSomethingToWaitFor(t *testing.T) {
	dir := t.TempDir()
	drv := &fakeDriver{}
	cs := connect(t, testConfig(t, dir), drv, NewArtifacts(dir))

	if res, text := call(t, cs, "wait_for", nil); !res.IsError || !strings.Contains(text, "text") {
		t.Fatalf("wait_for without arguments must explain itself: %s", text)
	}
	call(t, cs, "wait_for", map[string]any{"text": "Заказ оформлен"})
	if drv.actions[0] != "waitText Заказ оформлен" {
		t.Fatalf("actions: %v", drv.actions)
	}
}
