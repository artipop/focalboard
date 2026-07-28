package webtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
)

// The fixture is deliberately ordinary: a form, a button that rewrites the page,
// a console error and a request the server refuses — the four things a tester
// looks at.
const fixture = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>Магазин</title></head>
<body>
  <h1>Каталог</h1>
  <p>Товары в наличии</p>
  <label for="q">Поиск</label>
  <input id="q" name="q" placeholder="Что ищем?">
  <select id="size"><option>S</option><option>M</option></select>
  <button id="buy" onclick="checkout()">Оформить заказ</button>
  <div id="result"></div>
  <script>
    console.error('boom: cart is undefined')
    fetch('/api/missing')
    function checkout() { document.getElementById('result').textContent = 'Заказ оформлен' }
  </script>
</body></html>`

// launchTestBrowser starts a real headless browser, or skips: CI without an
// installed Chrome should not download 150 MB to run a unit test.
func launchTestBrowser(t *testing.T, base string) *Browser {
	t.Helper()
	if _, ok := launcher.LookPath(); !ok {
		t.Skip("no Chrome/Chromium installed; skipping the real-browser test")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Launch(context.Background(), Config{
		BaseURL: u, Headless: true, Width: 1000, Height: 700,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestBrowserDrivesARealPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	b := launchTestBrowser(t, srv.URL)
	ctx := context.Background()
	if err := b.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	snap, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, want := range []string{"title: Магазин", `h1 "Каталог"`, "Оформить заказ", "input"} {
		if !strings.Contains(snap, want) {
			t.Fatalf("snapshot is missing %q:\n%s", want, snap)
		}
	}

	buy := refOf(t, snap, "Оформить заказ")
	if err := b.Click(ctx, buy); err != nil {
		t.Fatalf("click: %v", err)
	}
	if err := b.WaitText(ctx, "Заказ оформлен", 5*time.Second); err != nil {
		t.Fatalf("the click did not change the page: %v", err)
	}

	// The input is found through the same refs, and typing into it sticks.
	snap, err = b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Fill(ctx, refOf(t, snap, "Что ищем?"), "чайник"); err != nil {
		t.Fatalf("fill: %v", err)
	}
	value, err := b.Eval(ctx, `() => document.getElementById('q').value`)
	if err != nil || !strings.Contains(value, "чайник") {
		t.Fatalf("value after fill: %q, %v", value, err)
	}

	if png, err := b.Screenshot(ctx, false); err != nil || len(png) < 100 || string(png[1:4]) != "PNG" {
		t.Fatalf("screenshot: %d bytes, %v", len(png), err)
	}

	// A stale ref is the normal result of a re-render, and must say so.
	if err := b.Click(ctx, "e999"); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("a stale ref should ask for a new snapshot, got: %v", err)
	}
}

func TestBrowserRecordsConsoleAndNetworkFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	b := launchTestBrowser(t, srv.URL)
	if err := b.Navigate(context.Background(), srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	// The fetch and the console call happen while the page loads; give the
	// events a moment to arrive.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(b.ConsoleLog()) > 0 && len(b.NetworkLog()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	var consoleText string
	for _, e := range b.ConsoleLog() {
		consoleText += e.String() + "\n"
	}
	if !strings.Contains(consoleText, "boom") {
		t.Fatalf("console log: %s", consoleText)
	}
	var netText string
	for _, e := range b.NetworkLog() {
		netText += e.String() + "\n"
	}
	if !strings.Contains(netText, "404") || !strings.Contains(netText, "/api/missing") {
		t.Fatalf("network log: %s", netText)
	}
}

// refOf pulls the ref out of the snapshot line containing text.
func refOf(t *testing.T, snapshot, text string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, text) {
			continue
		}
		open := strings.LastIndex(line, "[")
		closing := strings.LastIndex(line, "]")
		if open >= 0 && closing > open {
			return line[open+1 : closing]
		}
	}
	t.Fatalf("no ref for %q in:\n%s", text, snapshot)
	return ""
}
