package webtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Browser is the real Driver: one Chrome, one tab, for the length of one test
// session.
type Browser struct {
	cfg     Config
	browser *rod.Browser
	page    *rod.Page
	cleanup func()

	mu       sync.Mutex
	console  []ConsoleEntry
	network  []NetworkEntry
	inflight map[proto.NetworkRequestID]request
}

// request remembers what a request was for, so a failure — which CDP reports by
// id only — can be logged as a method and a URL.
type request struct {
	method string
	url    string
}

var _ Driver = (*Browser)(nil)

// logLimit bounds what one run keeps in memory; the tail is what matters when
// something breaks, so the oldest entries are dropped first.
const logLimit = 500

// Launch starts a browser for cfg. The binary is the configured one, else an
// installed Chrome/Chromium/Edge, else a managed Chromium downloaded once into
// the data directory — so a fresh machine needs no setup.
func Launch(ctx context.Context, cfg Config) (*Browser, error) {
	l := launcher.New().
		Headless(cfg.Headless).
		Leakless(true).
		Set("disable-gpu").
		Set("no-first-run").
		Set("disable-features", "Translate,MediaRouter").
		Set("window-size", fmt.Sprintf("%d,%d", cfg.Width, cfg.Height))
	if bin, err := resolveBrowserBinary(cfg.Browser); err != nil {
		return nil, err
	} else if bin != "" {
		l = l.Bin(bin)
	}
	if cfg.UserData != "" {
		l = l.UserDataDir(cfg.UserData)
	}

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("не удалось запустить браузер: %w", err)
	}
	br := rod.New().ControlURL(controlURL).Context(ctx)
	if err := br.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("не удалось подключиться к браузеру: %w", err)
	}
	page, err := br.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = br.Close()
		l.Kill()
		return nil, fmt.Errorf("не удалось открыть вкладку: %w", err)
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: cfg.Width, Height: cfg.Height, DeviceScaleFactor: 1,
	}); err != nil {
		_ = br.Close()
		l.Kill()
		return nil, fmt.Errorf("не удалось задать размер окна: %w", err)
	}

	b := &Browser{cfg: cfg, browser: br, page: page, inflight: map[proto.NetworkRequestID]request{}, cleanup: func() {
		_ = br.Close()
		l.Kill()
	}}
	b.watch()
	return b, nil
}

// resolveBrowserBinary returns the browser to run, or "" to let rod download
// and manage one.
func resolveBrowserBinary(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("браузер не найден по пути %s: %w", explicit, err)
		}
		return explicit, nil
	}
	if found, ok := launcher.LookPath(); ok {
		return found, nil
	}
	return "", nil
}

// watch subscribes to the two event streams a tester actually needs: what the
// page printed, and which requests failed. Successful traffic is not recorded —
// it only buries the bug.
func (b *Browser) watch() {
	page := b.page
	go page.EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			b.addConsole(ConsoleEntry{At: time.Now(), Level: consoleLevel(e.Type), Text: consoleText(e)})
		},
		func(e *proto.RuntimeExceptionThrown) {
			text := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
				text = e.ExceptionDetails.Exception.Description
			}
			b.addConsole(ConsoleEntry{At: time.Now(), Level: "exception", Text: text})
		},
		func(e *proto.NetworkRequestWillBeSent) {
			b.mu.Lock()
			b.inflight[e.RequestID] = request{method: e.Request.Method, url: e.Request.URL}
			b.mu.Unlock()
		},
		func(e *proto.NetworkResponseReceived) {
			req := b.finish(e.RequestID)
			if interestingStatus(e.Response.Status) {
				b.addNetwork(NetworkEntry{
					At: time.Now(), Method: req.method, URL: e.Response.URL, Status: e.Response.Status,
				})
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			req := b.finish(e.RequestID)
			// A cancelled navigation is noise, not a defect.
			if e.Canceled {
				return
			}
			b.addNetwork(NetworkEntry{
				At: time.Now(), Method: req.method, URL: req.url, Error: e.ErrorText,
			})
		},
	)()
}

func consoleLevel(t proto.RuntimeConsoleAPICalledType) string {
	switch t {
	case proto.RuntimeConsoleAPICalledTypeError, proto.RuntimeConsoleAPICalledTypeAssert:
		return "error"
	case proto.RuntimeConsoleAPICalledTypeWarning:
		return "warn"
	default:
		return string(t)
	}
}

func consoleText(e *proto.RuntimeConsoleAPICalled) string {
	parts := make([]string, 0, len(e.Args))
	for _, arg := range e.Args {
		switch {
		case arg.Description != "":
			parts = append(parts, arg.Description)
		case arg.Value.Nil():
			parts = append(parts, string(arg.Type))
		default:
			parts = append(parts, arg.Value.JSON("", ""))
		}
	}
	return strings.Join(parts, " ")
}

// finish takes a request out of the in-flight table and returns what it was.
// An unknown id means the request started before we subscribed.
func (b *Browser) finish(id proto.NetworkRequestID) request {
	b.mu.Lock()
	defer b.mu.Unlock()
	req := b.inflight[id]
	delete(b.inflight, id)
	if req.url == "" {
		req.url = "(запрос неизвестен)"
	}
	return req
}

func (b *Browser) addConsole(e ConsoleEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.console = appendCapped(b.console, e)
}

func (b *Browser) addNetwork(e NetworkEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.network = appendCapped(b.network, e)
}

func appendCapped[T any](s []T, v T) []T {
	s = append(s, v)
	if len(s) > logLimit {
		s = s[len(s)-logLimit:]
	}
	return s
}

func (b *Browser) ConsoleLog() []ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ConsoleEntry(nil), b.console...)
}

func (b *Browser) NetworkLog() []NetworkEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]NetworkEntry(nil), b.network...)
}

func (b *Browser) Close() error {
	if b.cleanup != nil {
		b.cleanup()
	}
	return nil
}

// ctxPage binds the page to the call's context and default timeout, so a tool
// call can never hang the whole session.
func (b *Browser) ctxPage(ctx context.Context, timeout time.Duration) *rod.Page {
	return b.page.Context(ctx).Timeout(timeout)
}

// defaultTimeout bounds a single interaction; elementTimeout bounds resolving a
// ref, which rod would otherwise retry for the whole of it. A ref that is gone
// is usually gone for good — the page re-rendered — and the model should be
// told to take a new snapshot rather than sit through a long retry.
const (
	defaultTimeout = 30 * time.Second
	elementTimeout = 5 * time.Second
)

func (b *Browser) Navigate(ctx context.Context, url string) error {
	page := b.ctxPage(ctx, defaultTimeout)
	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("не удалось открыть %s: %w", url, err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("страница %s не загрузилась: %w", url, err)
	}
	return nil
}

func (b *Browser) URL(ctx context.Context) (string, error) {
	info, err := b.ctxPage(ctx, defaultTimeout).Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func (b *Browser) Snapshot(ctx context.Context) (string, error) {
	res, err := b.ctxPage(ctx, defaultTimeout).Eval(snapshotJS, snapshotMaxLines)
	if err != nil {
		return "", fmt.Errorf("не удалось снять состояние страницы: %w", err)
	}
	return res.Value.Str(), nil
}

// element resolves a ref from the last snapshot. A ref that no longer exists is
// the normal consequence of a re-render, so the error says what to do about it.
func (b *Browser) element(ctx context.Context, ref string) (*rod.Element, error) {
	el, err := b.ctxPage(ctx, elementTimeout).Element(refSelector(ref))
	if err != nil {
		return nil, fmt.Errorf("элемент %s не найден — страница изменилась, сделай новый snapshot", ref)
	}
	return el, nil
}

func (b *Browser) Click(ctx context.Context, ref string) error {
	el, err := b.element(ctx, ref)
	if err != nil {
		return err
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("не удалось прокрутить до %s: %w", ref, err)
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("не удалось кликнуть по %s: %w", ref, err)
	}
	return nil
}

func (b *Browser) Fill(ctx context.Context, ref, text string) error {
	el, err := b.element(ctx, ref)
	if err != nil {
		return err
	}
	if err := el.SelectAllText(); err != nil {
		// An empty field has nothing to select; typing into it still works.
		_ = err
	}
	if err := el.Input(text); err != nil {
		return fmt.Errorf("не удалось ввести текст в %s: %w", ref, err)
	}
	return nil
}

func (b *Browser) SelectOption(ctx context.Context, ref, value string) error {
	el, err := b.element(ctx, ref)
	if err != nil {
		return err
	}
	if err := el.Select([]string{value}, true, rod.SelectorTypeText); err != nil {
		return fmt.Errorf("не удалось выбрать %q в %s: %w", value, ref, err)
	}
	return nil
}

func (b *Browser) Hover(ctx context.Context, ref string) error {
	el, err := b.element(ctx, ref)
	if err != nil {
		return err
	}
	if err := el.Hover(); err != nil {
		return fmt.Errorf("не удалось навести курсор на %s: %w", ref, err)
	}
	return nil
}

func (b *Browser) Press(ctx context.Context, key string) error {
	k, err := parseKey(key)
	if err != nil {
		return err
	}
	if err := b.ctxPage(ctx, defaultTimeout).Keyboard.Type(k); err != nil {
		return fmt.Errorf("не удалось нажать %s: %w", key, err)
	}
	return nil
}

// keys are the named keys press_key accepts; anything else is either a single
// character or a mistake worth reporting.
var keys = map[string]input.Key{
	"enter":      input.Enter,
	"escape":     input.Escape,
	"esc":        input.Escape,
	"tab":        input.Tab,
	"backspace":  input.Backspace,
	"delete":     input.Delete,
	"space":      input.Space,
	"home":       input.Home,
	"end":        input.End,
	"pageup":     input.PageUp,
	"pagedown":   input.PageDown,
	"arrowup":    input.ArrowUp,
	"arrowdown":  input.ArrowDown,
	"arrowleft":  input.ArrowLeft,
	"arrowright": input.ArrowRight,
	"up":         input.ArrowUp,
	"down":       input.ArrowDown,
	"left":       input.ArrowLeft,
	"right":      input.ArrowRight,
}

func parseKey(name string) (input.Key, error) {
	trimmed := strings.TrimSpace(name)
	if k, ok := keys[strings.ToLower(trimmed)]; ok {
		return k, nil
	}
	runes := []rune(trimmed)
	if len(runes) == 1 {
		return input.Key(runes[0]), nil
	}
	return 0, fmt.Errorf("неизвестная клавиша %q — допустимы Enter, Escape, Tab, Backspace, Delete, Space, Home, End, PageUp, PageDown, стрелки или один символ", name)
}

func (b *Browser) WaitText(ctx context.Context, text string, timeout time.Duration) error {
	page := b.ctxPage(ctx, timeout)
	err := rod.Try(func() {
		page.MustElementR("body", jsRegexLiteral(text))
	})
	if err != nil {
		return fmt.Errorf("текст %q не появился за %s", text, timeout)
	}
	return nil
}

func (b *Browser) WaitRef(ctx context.Context, ref string, timeout time.Duration) error {
	page := b.ctxPage(ctx, timeout)
	err := rod.Try(func() {
		page.MustElement(refSelector(ref))
	})
	if err != nil {
		return fmt.Errorf("элемент %s не появился за %s — возможно, нужен новый snapshot", ref, timeout)
	}
	return nil
}

func (b *Browser) WaitIdle(ctx context.Context, timeout time.Duration) error {
	if err := b.ctxPage(ctx, timeout+time.Second).WaitIdle(timeout); err != nil {
		return fmt.Errorf("страница не успокоилась за %s: %w", timeout, err)
	}
	return nil
}

// jsRegexLiteral turns a literal into the case-insensitive /pattern/flags form
// rod's ElementR expects — it is a JavaScript regex, not a Go one.
func jsRegexLiteral(text string) string {
	var b strings.Builder
	b.WriteString("/")
	for _, r := range text {
		if strings.ContainsRune(`\.+*?()|[]{}^$/`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	b.WriteString("/i")
	return b.String()
}

func (b *Browser) Screenshot(ctx context.Context, fullPage bool) ([]byte, error) {
	data, err := b.ctxPage(ctx, defaultTimeout).Screenshot(fullPage, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
	})
	if err != nil {
		return nil, fmt.Errorf("не удалось сделать скриншот: %w", err)
	}
	return data, nil
}

func (b *Browser) Eval(ctx context.Context, expression string) (string, error) {
	res, err := b.ctxPage(ctx, defaultTimeout).Eval(expression)
	if err != nil {
		return "", fmt.Errorf("не удалось выполнить выражение: %w", err)
	}
	if res.Value.Nil() {
		return string(res.Type), nil
	}
	return res.Value.JSON("", ""), nil
}
