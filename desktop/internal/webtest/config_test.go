package webtest

import (
	"strings"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://feat-x.preview.example.com")
	t.Setenv(EnvArtifacts, "/tmp/run1")
	t.Setenv(EnvViewport, "1440x900")
	t.Setenv(EnvSecrets, `{"user":"qa@example.com","password":"hunter2"}`)

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL.Host != "feat-x.preview.example.com" || cfg.Artifacts != "/tmp/run1" {
		t.Fatalf("cfg: %+v", cfg)
	}
	if cfg.Width != 1440 || cfg.Height != 900 {
		t.Fatalf("viewport: %dx%d", cfg.Width, cfg.Height)
	}
	if !cfg.Headless {
		t.Fatal("headless should be the default")
	}
	if v, ok := cfg.Secret("PASSWORD"); !ok || v != "hunter2" {
		t.Fatalf("secret lookup is case-sensitive: %q, %v", v, ok)
	}

	t.Setenv(EnvHeadless, "0")
	if cfg, _ := ConfigFromEnv(); cfg.Headless {
		t.Fatal("headless=0 should show the window")
	}
}

func TestConfigFromEnvRejectsNonsense(t *testing.T) {
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("an unset base URL must be an error")
	}
	t.Setenv(EnvBaseURL, "feat-x.example.com/app")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "абсолютным") {
		t.Fatalf("a scheme-less URL must be rejected: %v", err)
	}
	t.Setenv(EnvBaseURL, "https://feat-x.example.com")
	t.Setenv(EnvViewport, "huge")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("a malformed viewport must be an error")
	}
}

func TestResolveURLKeepsTheAgentOnThePreview(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://feat-x.example.com/app/")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct{ in, want string }{
		{"", "https://feat-x.example.com/app/"},
		{"checkout", "https://feat-x.example.com/app/checkout"},
		{"/checkout?x=1", "https://feat-x.example.com/checkout?x=1"},
		{"https://feat-x.example.com/admin", "https://feat-x.example.com/admin"},
	}
	for _, c := range cases {
		got, err := cfg.ResolveURL(c.in)
		if err != nil || got != c.want {
			t.Fatalf("ResolveURL(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}

	for _, bad := range []string{
		"https://evil.example.com/",
		"http://feat-x.example.com/",        // same host, other scheme
		"https://feat-x.example.com.evil/",  // prefix trick
		"https://other.feat-x.example.com/", // sibling subdomain
	} {
		if got, err := cfg.ResolveURL(bad); err == nil {
			t.Fatalf("ResolveURL(%q) = %q; want refusal", bad, got)
		}
	}
}

func TestNormalizeVerdict(t *testing.T) {
	for in, want := range map[string]string{
		"pass": VerdictPass, "Passed": VerdictPass, "прошёл": VerdictPass,
		"fail": VerdictFail, "FAILED": VerdictFail,
		"blocked": VerdictBlocked, "skipped": VerdictBlocked,
	} {
		got, err := NormalizeVerdict(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeVerdict(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeVerdict("почти прошёл"); err == nil {
		t.Fatal("an unknown verdict must be an error")
	}
}
