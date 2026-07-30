// Package dokku deploys a git branch to a Dokku host and exposes that as an MCP
// server, so a coding agent can deploy, inspect and tear down branch previews
// with its own tools instead of being handed a shell.
//
// One branch is one Dokku app: <baseApp>-<slug> served at that same name under
// the preview domain, which is the dokku host itself unless the target names
// another (Dokku infers the subdomain from the app name, so a preview per
// branch needs an app per branch). Everything happens over ssh from this side —
// apps:exists → apps:create → domains:set → git push — so nothing has to be
// installed on the host beyond a normal Dokku with our ssh key.
//
// The package knows nothing about Focalboard: it is handed a Target, a local
// repository path and a branch, which is exactly what the MCP process receives
// through its environment.
package dokku

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// The whole app name is one DNS label — <baseApp>-<slug>.<baseDomain> — and a
// label is 63 characters, which both halves have to fit inside once each has
// been folded. A folded half that had to be cut carries "-<6-char hash>", and a
// repository half may also carry the "r-" prefix, so the worst case is
// (2+16+7) + 1 + (30+7) = 63 exactly.
const (
	maxSlug      = 30 // branch half
	maxRepoLabel = 16 // repository half
)

// Target is one Dokku deployment destination: which host to talk to and how
// branch previews are named there. It is the JSON payload the MCP subprocess
// receives, and it is embedded in the desktop's deploy-target registry entry.
type Target struct {
	SSHHost string `json:"sshHost"`           // dokku host
	SSHUser string `json:"sshUser,omitempty"` // default "dokku"
	SSHPort int    `json:"sshPort,omitempty"` // default 22
	SSHKey  string `json:"sshKey,omitempty"`  // private key path; empty = agent/default keys

	// BaseApp prefixes every branch app, and the app name is the subdomain:
	// "api" → app api-<slug> at api-<slug>.<preview domain>. It is normally left
	// empty and derived from the repository name (WithBaseApp), so a target can
	// serve every repository pointed at it; set it only to override that.
	BaseApp string `json:"baseApp,omitempty"`

	// BaseDomain is the zone previews live under: "example.com" →
	// <baseApp>-<slug>.example.com. One wildcard record — *.example.com — covers
	// every preview, since the whole app name is one label.
	//
	// It is normally empty, because the machine dokku runs on is the machine the
	// previews are served from: the ssh host answers for both, which is Dokku's
	// own default too (a global domain falling back to the server's hostname).
	// Set it only where the two differ — ssh to an IP or through a bastion, apps
	// published under another zone.
	BaseDomain string `json:"baseDomain,omitempty"`
}

// Validate normalizes and checks a target. BaseApp may be empty here: the
// registry stores a target without one and the deploy path fills it from the
// repository (WithBaseApp), so New — where an actual deploy starts — is what
// insists on having it.
func (t Target) Validate() (Target, error) {
	t.SSHHost = strings.ToLower(strings.Trim(strings.TrimSpace(t.SSHHost), "."))
	t.SSHUser = strings.TrimSpace(t.SSHUser)
	t.SSHKey = strings.TrimSpace(t.SSHKey)
	t.BaseApp = strings.ToLower(strings.TrimSpace(t.BaseApp))
	t.BaseDomain = strings.ToLower(strings.Trim(strings.TrimSpace(t.BaseDomain), "."))

	if t.SSHHost == "" {
		return t, fmt.Errorf("не задан адрес Dokku-хоста")
	}
	if t.BaseApp != "" && !validAppName(t.BaseApp) {
		return t, fmt.Errorf("имя приложения %q должно начинаться с буквы и состоять из латиницы, цифр и дефисов", t.BaseApp)
	}
	if strings.ContainsAny(t.BaseDomain, " /:") {
		return t, fmt.Errorf("домен превью %q выглядит неверно: нужен только домен, например example.com", t.BaseDomain)
	}
	// Without a domain of its own the ssh host is the domain, which an address
	// cannot be built from.
	if t.BaseDomain == "" && net.ParseIP(t.SSHHost) != nil {
		return t, fmt.Errorf("хост задан ip-адресом (%s), поэтому нужен домен превью", t.SSHHost)
	}
	if t.SSHPort < 0 || t.SSHPort > 65535 {
		return t, fmt.Errorf("некорректный ssh-порт %d", t.SSHPort)
	}
	if t.SSHKey != "" && !filepath.IsAbs(t.SSHKey) {
		return t, fmt.Errorf("путь к ssh-ключу должен быть абсолютным: %s", t.SSHKey)
	}
	return t, nil
}

// User is the ssh user, defaulting to Dokku's own.
func (t Target) User() string {
	if u := strings.TrimSpace(t.SSHUser); u != "" {
		return u
	}
	return "dokku"
}

// Port is the ssh port, defaulting to 22.
func (t Target) Port() int {
	if t.SSHPort > 0 {
		return t.SSHPort
	}
	return 22
}

// Timeout bounds one push, build included.
func (t Target) Timeout() time.Duration {
	return 20 * time.Minute
}

// AppName is the Dokku app a branch slug is deployed as.
func (t Target) AppName(slug string) string {
	return t.BaseApp + "-" + slug
}

// PreviewDomain is the zone previews are served under: the ssh host itself
// unless the target names another.
func (t Target) PreviewDomain() string {
	if t.BaseDomain != "" {
		return t.BaseDomain
	}
	return t.SSHHost
}

// Domain is the hostname a branch slug is served at: the app name as a single
// label under the preview domain — <repo>-<branch>.example.com. One label is
// what makes a single wildcard record (*.example.com) cover every preview of
// every repository, and it is also Dokku's own convention, the vhost being the
// app name under a global domain.
func (t Target) Domain(slug string) string {
	return t.AppName(slug) + "." + t.PreviewDomain()
}

// WithBaseApp derives the missing BaseApp from the repository the branch is
// pushed from, which is what makes one target serve several repositories. An
// explicit BaseApp wins, and an empty repository name changes nothing — New
// then reports the target as unusable rather than building "-<slug>".
func (t Target) WithBaseApp(repoName string) Target {
	if t.BaseApp != "" {
		return t
	}
	t.BaseApp = AppLabel(repoName)
	return t
}

// AppLabel turns a repository name into the leading half of an app name: the
// same folding as a branch slug on a shorter budget, plus a leading letter,
// which Dokku requires of an app name and a name like "2fa-service" lacks.
//
// TODO: validate the name where it is entered — the repository registry — so a
// name that cannot be a hostname is rejected there instead of being quietly
// folded here, which is how two repositories could end up on one subdomain.
func AppLabel(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	label := foldLabel(name, maxRepoLabel, "r")
	if label[0] < 'a' || label[0] > 'z' {
		return "r-" + label
	}
	return label
}

// URL is the address to open in a browser.
// A preview is plain http: TLS is per app rather than per host, so it belongs
// with the other per-repository settings whenever those land.
func (t Target) URL(slug string) string {
	return "http://" + t.Domain(slug)
}

// AppSlug turns a branch name into the label a preview is named after:
// "feature/Add_Login" → "feature-add-login". Everything outside [a-z0-9]
// collapses into a single dash, because the result is both a Dokku app name
// component and a DNS label.
//
// Two branches must never collapse onto one app, so a slug that had to be cut
// short — or that lost everything (a fully non-ASCII branch name) — carries a
// short hash of the original name.
func AppSlug(branch string) string {
	return foldLabel(branch, maxSlug, "b")
}

// foldLabel folds an arbitrary name into a DNS label of at most max characters,
// falling back to fallback-<hash> when nothing usable is left.
func foldLabel(name string, max int, fallback string) string {
	trimmed := strings.TrimSpace(name)
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(trimmed) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	label := strings.Trim(b.String(), "-")
	switch {
	case label == "":
		return fallback + "-" + shortHash(trimmed)
	case len(label) > max:
		return strings.Trim(label[:max], "-") + "-" + shortHash(trimmed)
	default:
		return label
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:6]
}

// validAppName mirrors Dokku's own rule: start with a lowercase letter, then
// lowercase letters, digits and dashes.
func validAppName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}
