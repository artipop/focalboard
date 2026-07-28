// Package webtest drives a real browser over CDP and exposes it to a coding
// agent as an MCP server, so a card moved into the test column can be checked
// against its deployed preview without a human clicking through it.
//
// Like internal/dokku, everything the server may touch — which site, where the
// artifacts go, which credentials exist — arrives through the environment
// rather than tool arguments: the model chooses the steps of the scenario, not
// the target.
package webtest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// ServerName is how the tools appear to an agent: mcp__webtest__snapshot etc.
const ServerName = "webtest"

// Environment the MCP process is configured through.
const (
	EnvBaseURL   = "FOCALBOARD_WEBTEST_BASE_URL"  // preview address; the only allowed origin
	EnvArtifacts = "FOCALBOARD_WEBTEST_ARTIFACTS" // directory for screenshots and result.json
	EnvBrowser   = "FOCALBOARD_WEBTEST_BROWSER"   // explicit browser binary
	EnvHeadless  = "FOCALBOARD_WEBTEST_HEADLESS"  // "0" shows the window
	EnvViewport  = "FOCALBOARD_WEBTEST_VIEWPORT"  // WxH, default 1280x800
	EnvSecrets   = "FOCALBOARD_WEBTEST_SECRETS"   // JSON name → value for fill_secret
	EnvUserData  = "FOCALBOARD_WEBTEST_USER_DATA" // browser profile dir (kept per session)
)

// ResultFile is the name report_result writes inside the artifacts directory.
// The session reads it back to decide where the card goes.
const ResultFile = "result.json"

// ScreenshotDir holds the evidence, relative to the artifacts directory.
const ScreenshotDir = "screenshots"

// Config is the resolved environment of one test run.
type Config struct {
	BaseURL   *url.URL
	Artifacts string
	Browser   string
	Headless  bool
	Width     int
	Height    int
	Secrets   map[string]string
	UserData  string
}

// ConfigFromEnv reads the process environment. Only the base URL is required:
// a run with nowhere to write artifacts still answers questions about the page.
func ConfigFromEnv() (Config, error) {
	raw := strings.TrimSpace(os.Getenv(EnvBaseURL))
	if raw == "" {
		return Config{}, fmt.Errorf("не задан %s", EnvBaseURL)
	}
	base, err := url.Parse(raw)
	if err != nil {
		return Config{}, fmt.Errorf("не удалось разобрать %s (%q): %w", EnvBaseURL, raw, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return Config{}, fmt.Errorf("%s должен быть абсолютным адресом, а не %q", EnvBaseURL, raw)
	}

	cfg := Config{
		BaseURL:   base,
		Artifacts: strings.TrimSpace(os.Getenv(EnvArtifacts)),
		Browser:   strings.TrimSpace(os.Getenv(EnvBrowser)),
		Headless:  os.Getenv(EnvHeadless) != "0",
		Width:     1280,
		Height:    800,
		UserData:  strings.TrimSpace(os.Getenv(EnvUserData)),
	}
	if v := strings.TrimSpace(os.Getenv(EnvViewport)); v != "" {
		w, h, err := parseViewport(v)
		if err != nil {
			return Config{}, err
		}
		cfg.Width, cfg.Height = w, h
	}
	if v := strings.TrimSpace(os.Getenv(EnvSecrets)); v != "" {
		if err := json.Unmarshal([]byte(v), &cfg.Secrets); err != nil {
			return Config{}, fmt.Errorf("не удалось разобрать %s: %w", EnvSecrets, err)
		}
	}
	return cfg, nil
}

func parseViewport(v string) (int, int, error) {
	parts := strings.Split(strings.ToLower(v), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%s должен быть вида 1280x800, а не %q", EnvViewport, v)
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("%s должен быть вида 1280x800, а не %q", EnvViewport, v)
	}
	return w, h, nil
}

// ResolveURL turns what the model asked for into an address it is allowed to
// open: a path is resolved against the preview, an absolute URL has to be the
// same origin. This is the whole of the "the agent cannot wander off" policy.
func (c Config) ResolveURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return c.BaseURL.String(), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("не удалось разобрать адрес %q: %w", raw, err)
	}
	if !u.IsAbs() {
		return c.BaseURL.ResolveReference(u).String(), nil
	}
	if !strings.EqualFold(u.Scheme, c.BaseURL.Scheme) || !strings.EqualFold(u.Host, c.BaseURL.Host) {
		return "", fmt.Errorf("можно открывать только страницы %s://%s — адрес %q ведёт в другое место",
			c.BaseURL.Scheme, c.BaseURL.Host, raw)
	}
	return u.String(), nil
}

// Secret looks a credential up by name. The value never passes through the
// model: fill_secret takes the name and types the value into the page.
func (c Config) Secret(name string) (string, bool) {
	for k, v := range c.Secrets {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return v, true
		}
	}
	return "", false
}

// SecretNames lists what fill_secret can be asked for, so the instructions can
// tell the model which credentials exist without revealing them.
func (c Config) SecretNames() []string {
	names := make([]string, 0, len(c.Secrets))
	for k := range c.Secrets {
		names = append(names, k)
	}
	return names
}
