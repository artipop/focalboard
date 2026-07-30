package dokku

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Runner executes one command and returns its combined output. It is the single
// seam the whole package is tested through: no test ever spawns ssh or git.
type Runner func(ctx context.Context, dir string, env []string, name string, args ...string) (string, error)

// ExitError is a command that ran and failed, carrying the status so callers can
// tell "the app does not exist" (dokku's own non-zero) from "ssh could not
// connect" (255).
type ExitError struct {
	Code   int
	Output string
	Err    error
}

func (e *ExitError) Error() string {
	out := strings.TrimSpace(e.Output)
	if out == "" {
		return fmt.Sprintf("команда завершилась с кодом %d", e.Code)
	}
	return fmt.Sprintf("команда завершилась с кодом %d: %s", e.Code, out)
}

func (e *ExitError) Unwrap() error { return e.Err }

// sshConnectFailed is ssh's own exit status, distinct from anything the remote
// command can return.
const sshConnectFailed = 255

// Exec is the real Runner.
func Exec(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return text, &ExitError{Code: ee.ExitCode(), Output: text, Err: err}
		}
		return text, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(text))
	}
	return text, nil
}

// Client talks to one Dokku target on behalf of one local repository.
type Client struct {
	Target Target
	Repo   string // local git repository the branch is pushed from
	Branch string // branch used when a tool call omits one
	Run    Runner // nil → Exec
}

// New validates the target and returns a client. repo may be empty for a client
// that only inspects (logs, status, list).
func New(t Target, repo, branch string) (*Client, error) {
	t, err := t.Validate()
	if err != nil {
		return nil, err
	}
	if t.BaseApp == "" {
		return nil, fmt.Errorf("у цели не определено имя приложения: оно берётся из имени репозитория, задайте его явно (baseApp)")
	}
	return &Client{Target: t, Repo: repo, Branch: strings.TrimSpace(branch)}, nil
}

func (c *Client) run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	r := c.Run
	if r == nil {
		r = Exec
	}
	return r(ctx, dir, env, name, args...)
}

// sshArgs builds the ssh invocation for a dokku command. BatchMode turns a
// missing/rejected key into an error instead of a password prompt nobody is
// there to answer — this runs unattended under an agent.
func (c *Client) sshArgs(dokkuArgs ...string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
	}
	if c.Target.SSHKey != "" {
		args = append(args, "-i", c.Target.SSHKey, "-o", "IdentitiesOnly=yes")
	}
	if p := c.Target.Port(); p != 22 {
		args = append(args, "-p", strconv.Itoa(p))
	}
	args = append(args, c.Target.User()+"@"+c.Target.SSHHost)
	return append(args, dokkuArgs...)
}

// dokku runs one dokku command on the host.
func (c *Client) dokku(ctx context.Context, args ...string) (string, error) {
	out, err := c.run(ctx, "", nil, "ssh", c.sshArgs(args...)...)
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) && ee.Code == sshConnectFailed {
			return out, fmt.Errorf("не удалось подключиться к %s по ssh: %s", c.Target.SSHHost, strings.TrimSpace(firstLines(out, 5)))
		}
		return out, err
	}
	return out, nil
}

// AppExists reports whether the branch app is already on the host. Dokku answers
// with its exit status, so only ssh's own 255 is a real error here.
func (c *Client) AppExists(ctx context.Context, app string) (bool, error) {
	out, err := c.dokku(ctx, "apps:exists", app)
	if err == nil {
		return true, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code != sshConnectFailed {
		return false, nil
	}
	_ = out
	return false, err
}

// EnsureApp creates the branch app when it is missing and points its domain at
// the branch. Returns whether it had to create it.
func (c *Client) EnsureApp(ctx context.Context, slug string) (created bool, err error) {
	app := c.Target.AppName(slug)
	exists, err := c.AppExists(ctx, app)
	if err != nil {
		return false, err
	}
	if !exists {
		if _, err := c.dokku(ctx, "apps:create", app); err != nil {
			return false, fmt.Errorf("не удалось создать приложение %s: %w", app, err)
		}
		created = true
	}
	if _, err := c.dokku(ctx, "domains:set", app, c.Target.Domain(slug)); err != nil {
		return created, fmt.Errorf("не удалось назначить домен %s: %w", c.Target.Domain(slug), err)
	}
	return created, nil
}

// Result is one completed deployment.
type Result struct {
	App      string   `json:"app"`
	Branch   string   `json:"branch"`
	URL      string   `json:"url"`
	Created  bool     `json:"created"`
	PushLog  string   `json:"pushLog"`
	Warnings []string `json:"warnings,omitempty"`
}

// Deploy creates the app if needed and pushes the branch to it. Dokku builds
// what lands on the app's deploy branch, so the local branch is pushed onto the
// remote master ref; --force keeps a rebased branch deployable.
func (c *Client) Deploy(ctx context.Context, branch string) (Result, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = c.Branch
	}
	if branch == "" {
		return Result{}, fmt.Errorf("не указана ветка для деплоя")
	}
	if c.Repo == "" {
		return Result{}, fmt.Errorf("не задан локальный репозиторий")
	}
	slug := AppSlug(branch)
	app := c.Target.AppName(slug)
	res := Result{App: app, Branch: branch, URL: c.Target.URL(slug)}

	created, err := c.EnsureApp(ctx, slug)
	res.Created = created
	if err != nil {
		return res, err
	}

	remote := fmt.Sprintf("ssh://%s@%s:%d/%s", c.Target.User(), c.Target.SSHHost, c.Target.Port(), app)
	out, err := c.run(ctx, c.Repo, c.gitEnv(), "git", "push", "--force", remote, branch+":refs/heads/master")
	res.PushLog = lastLines(out, 120)
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("деплой не уложился в отведённое время (%s); sessionTimeoutMinutes в конфиге агента должен быть больше: %w", c.Target.Timeout(), err)
		}
		return res, fmt.Errorf("git push в %s не прошёл: %w", app, err)
	}

	return res, nil
}

// gitEnv points git's ssh at the target's key and keeps it non-interactive.
func (c *Client) gitEnv() []string {
	cmd := "ssh -o BatchMode=yes -o ConnectTimeout=15"
	if c.Target.SSHKey != "" {
		cmd += " -i " + shellQuote(c.Target.SSHKey) + " -o IdentitiesOnly=yes"
	}
	return []string{"GIT_SSH_COMMAND=" + cmd, "GIT_TERMINAL_PROMPT=0"}
}

// Logs returns the tail of an app's logs.
func (c *Client) Logs(ctx context.Context, slug string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	return c.dokku(ctx, "logs", c.Target.AppName(slug), "-n", strconv.Itoa(lines))
}

// Status reports the running processes of an app.
func (c *Client) Status(ctx context.Context, slug string) (string, error) {
	return c.dokku(ctx, "ps:report", c.Target.AppName(slug))
}

// Destroy removes a branch app from the host.
func (c *Client) Destroy(ctx context.Context, slug string) (string, error) {
	return c.dokku(ctx, "apps:destroy", c.Target.AppName(slug), "--force")
}

// List returns the branch apps of this target (the base app itself excluded).
func (c *Client) List(ctx context.Context) ([]string, error) {
	out, err := c.dokku(ctx, "apps:list", "--format", "json")
	if err != nil {
		return nil, err
	}
	prefix := c.Target.BaseApp + "-"
	var apps []string
	// --format json is the documented shape; fall back to the plain listing
	// (which carries a "=====> My Apps" header) when a host answers with it.
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &apps); err != nil {
		apps = nil
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "=") || strings.Contains(line, " ") {
				continue
			}
			apps = append(apps, line)
		}
	}
	var out2 []string
	for _, a := range apps {
		if strings.HasPrefix(a, prefix) {
			out2 = append(out2, a)
		}
	}
	sort.Strings(out2)
	return out2, nil
}

// CurrentBranch is the branch checked out in the local repository.
func CurrentBranch(ctx context.Context, run Runner, repo string) (string, error) {
	if run == nil {
		run = Exec
	}
	out, err := run(ctx, repo, nil, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("не удалось определить текущую ветку в %s: %w", repo, err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("репозиторий %s не на ветке (detached HEAD) — укажи ветку явно", repo)
	}
	return branch, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// shellQuote wraps a path for GIT_SSH_COMMAND, which git splits shell-style.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
