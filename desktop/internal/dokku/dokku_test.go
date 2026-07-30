package dokku

import (
	"context"
	"strings"
	"testing"
)

// call is one command a test runner saw.
type call struct {
	dir  string
	env  []string
	name string
	args []string
}

// fakeRunner records calls and answers them from a script keyed by a substring
// of the command line; anything unmatched succeeds with empty output.
type fakeRunner struct {
	calls   []call
	replies map[string]reply
}

type reply struct {
	out string
	err error
}

func (f *fakeRunner) run(_ context.Context, dir string, env []string, name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{dir: dir, env: env, name: name, args: args})
	line := name + " " + strings.Join(args, " ")
	for key, r := range f.replies {
		if strings.Contains(line, key) {
			return r.out, r.err
		}
	}
	return "", nil
}

func (f *fakeRunner) line(i int) string {
	c := f.calls[i]
	return c.name + " " + strings.Join(c.args, " ")
}

func testTarget() Target {
	return Target{SSHHost: "dokku.example.com", BaseApp: "api", BaseDomain: "example.com"}
}

func TestAppSlug(t *testing.T) {
	cases := []struct{ branch, want string }{
		{"main", "main"},
		{"feature/Add_Login", "feature-add-login"},
		{"FEAT/переход", "feat"}, // non-ASCII drops out, ASCII part survives
		{"  spaces  here ", "spaces-here"},
		{"---dashes---", "dashes"},
		{"2fix", "2fix"},
	}
	for _, c := range cases {
		if got := AppSlug(c.branch); got != c.want {
			t.Errorf("AppSlug(%q) = %q, want %q", c.branch, got, c.want)
		}
	}

	// A branch with nothing ASCII in it still needs a unique, valid label.
	cyrillic := AppSlug("ветка")
	if !strings.HasPrefix(cyrillic, "b-") || len(cyrillic) != 8 {
		t.Errorf("AppSlug(cyrillic) = %q, want a b-<hash> label", cyrillic)
	}
	if AppSlug("ветка") == AppSlug("другая") {
		t.Error("two different non-ASCII branches collapsed onto one slug")
	}

	// Long branches are cut but must not collide on their shared prefix.
	long1 := AppSlug("feature/a-very-long-branch-name-number-one")
	long2 := AppSlug("feature/a-very-long-branch-name-number-two")
	if long1 == long2 {
		t.Errorf("long branches collided: %q", long1)
	}
	if len(long1) > maxSlug+7 {
		t.Errorf("slug %q longer than the cap", long1)
	}
}

func TestTargetValidate(t *testing.T) {
	if _, err := (Target{BaseApp: "api", BaseDomain: "x.com"}).Validate(); err == nil {
		t.Error("expected an error without an ssh host")
	}
	if _, err := (Target{SSHHost: "h", BaseApp: "1api", BaseDomain: "x.com"}).Validate(); err == nil {
		t.Error("expected an error for an app name starting with a digit")
	}
	// The ssh host doubles as the preview domain, so a target needs no domain
	// of its own — unless the host is an ip address, which cannot carry one.
	if _, err := (Target{SSHHost: "dokku.example.com", BaseApp: "api"}).Validate(); err != nil {
		t.Errorf("a target without a preview domain should validate: %v", err)
	}
	if _, err := (Target{SSHHost: "10.0.0.7", BaseApp: "api"}).Validate(); err == nil {
		t.Error("expected an error for an ip host with no preview domain")
	}
	if _, err := (Target{SSHHost: "h", BaseApp: "api", BaseDomain: "https://x.com"}).Validate(); err == nil {
		t.Error("expected an error for a domain given as a URL")
	}
	if _, err := (Target{SSHHost: "h", BaseApp: "api", BaseDomain: "x.com", SSHKey: "key.pem"}).Validate(); err == nil {
		t.Error("expected an error for a relative ssh key path")
	}

	// The app name is derived from the repository, so a target without one is
	// valid in the registry — and unusable only once a deploy needs it.
	if _, err := (Target{SSHHost: "host.example.com", BaseDomain: "x.com"}).Validate(); err != nil {
		t.Errorf("a target without a base app should validate: %v", err)
	}
	if _, err := New(Target{SSHHost: "h", BaseDomain: "x.com"}, "/repo", "main"); err == nil {
		t.Error("expected New to reject a target with no app name")
	}

	got, err := (Target{SSHHost: " h ", BaseApp: " API ", BaseDomain: " X.com. "}).Validate()
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.BaseApp != "api" || got.BaseDomain != "x.com" || got.SSHHost != "h" {
		t.Errorf("normalization: %+v", got)
	}
	if got.AppName("feat") != "api-feat" || got.Domain("feat") != "api-feat.x.com" || got.URL("feat") != "http://api-feat.x.com" {
		t.Errorf("naming: %s %s %s", got.AppName("feat"), got.Domain("feat"), got.URL("feat"))
	}
}

func TestPreviewDomainFallsBackToTheHost(t *testing.T) {
	host, err := (Target{SSHHost: "dokku.example.com", BaseApp: "api"}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	if got := host.Domain("feat"); got != "api-feat.dokku.example.com" {
		t.Errorf("hostname %q, want the ssh host as the domain", got)
	}
	own, err := (Target{SSHHost: "dokku.example.com", BaseApp: "api", BaseDomain: "preview.example.com"}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	if got := own.Domain("feat"); got != "api-feat.preview.example.com" {
		t.Errorf("hostname %q, want the target's own domain", got)
	}
}

func TestWithBaseApp(t *testing.T) {
	base := Target{SSHHost: "h", BaseDomain: "example.com"}

	got := base.WithBaseApp("My Project")
	if got.BaseApp != "my-project" || got.Domain("feat") != "my-project-feat.example.com" {
		t.Errorf("derived app/domain: %q %q", got.BaseApp, got.Domain("feat"))
	}

	// A repository whose name cannot start an app name still gets a valid one.
	if got := base.WithBaseApp("2fa-service"); got.BaseApp != "r-2fa-service" {
		t.Errorf("leading digit: %q", got.BaseApp)
	}

	// An explicit name wins, and nothing is invented without a repository.
	if got := (Target{SSHHost: "h", BaseApp: "api", BaseDomain: "example.com"}).WithBaseApp("other"); got.BaseApp != "api" {
		t.Errorf("explicit base app overwritten: %q", got.BaseApp)
	}
	if got := base.WithBaseApp("  "); got.BaseApp != "" {
		t.Errorf("empty repository name produced %q", got.BaseApp)
	}

	// The whole app name is one DNS label, so the two halves together must stay
	// inside 63 characters however long the repository and the branch are — the
	// worst case being a repository name that also needs the "r-" prefix.
	long := base.WithBaseApp("9-repository-with-an-unreasonably-long-name")
	app := long.AppName(AppSlug("feature/an-unreasonably-long-branch-name-as-well"))
	if len(app) > 63 {
		t.Errorf("app name %q is %d characters, over the DNS label limit", app, len(app))
	}

	// Two long repository names must not fold onto one label.
	if AppLabel("a-repository-with-an-unreasonably-long-name") == AppLabel("a-repository-with-an-unreasonably-long-name-too") {
		t.Error("two long repository names collided")
	}
}

func TestDeployCreatesAppAndPushes(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		// The app is missing: dokku answers apps:exists with a non-zero status.
		"apps:exists": {out: "app does not exist", err: &ExitError{Code: 1}},
		"git push":    {out: "remote: build\nremote: done\nTo ssh://…\n"},
	}}
	cl, err := New(testTarget(), "/repo", "feat/login")
	if err != nil {
		t.Fatal(err)
	}
	cl.Run = f.run

	res, err := cl.Deploy(context.Background(), "")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if res.App != "api-feat-login" || res.URL != "http://api-feat-login.example.com" || !res.Created {
		t.Fatalf("result: %+v", res)
	}
	if len(f.calls) != 4 {
		t.Fatalf("expected exists/create/domains/push, got %d calls: %v", len(f.calls), f.calls)
	}
	if !strings.Contains(f.line(1), "apps:create api-feat-login") {
		t.Errorf("create call: %s", f.line(1))
	}
	if !strings.Contains(f.line(2), "domains:set api-feat-login api-feat-login.example.com") {
		t.Errorf("domains call: %s", f.line(2))
	}
	push := f.calls[3]
	if push.name != "git" || push.dir != "/repo" {
		t.Errorf("push ran as %q in %q", push.name, push.dir)
	}
	if !strings.Contains(f.line(3), "ssh://dokku@dokku.example.com:22/api-feat-login feat/login:refs/heads/master") {
		t.Errorf("push args: %s", f.line(3))
	}
}

func TestDeployExistingAppSkipsCreate(t *testing.T) {
	f := &fakeRunner{}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	res, err := cl.Deploy(context.Background(), "")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if res.Created {
		t.Error("existing app reported as created")
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.args, " "), "apps:create") {
			t.Errorf("apps:create called for an existing app: %v", c.args)
		}
	}
}

func TestSSHConnectionFailureIsNotAMissingApp(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"apps:exists": {out: "ssh: connect to host dokku.example.com port 22: Connection refused",
			err: &ExitError{Code: sshConnectFailed}},
	}}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	if _, err := cl.Deploy(context.Background(), ""); err == nil {
		t.Fatal("expected a connection error, deploy reported success")
	} else if !strings.Contains(err.Error(), "ssh") {
		t.Errorf("error should name the ssh failure: %v", err)
	}
	if len(f.calls) != 1 {
		t.Errorf("deploy continued past a dead host: %v", f.calls)
	}
}

func TestPushFailureCarriesTheLog(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"git push": {out: "remote: Building…\nremote: ERROR: no Procfile\n", err: &ExitError{Code: 1}},
	}}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	res, err := cl.Deploy(context.Background(), "")
	if err == nil {
		t.Fatal("expected a push error")
	}
	if !strings.Contains(res.PushLog, "no Procfile") {
		t.Errorf("push log lost: %q", res.PushLog)
	}
}

func TestSSHArgsAndGitEnvUseTheKey(t *testing.T) {
	target := testTarget()
	target.SSHKey = "/home/me/keys/id ed25519"
	target.SSHPort = 2222
	target.SSHUser = "deploy"
	cl, err := New(target, "/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cl.sshArgs("apps:list"), " ")
	for _, want := range []string{"BatchMode=yes", "-i /home/me/keys/id ed25519", "IdentitiesOnly=yes", "-p 2222", "deploy@dokku.example.com apps:list"} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh args %q missing %q", args, want)
		}
	}
	env := strings.Join(cl.gitEnv(), " ")
	if !strings.Contains(env, "GIT_SSH_COMMAND=") || !strings.Contains(env, "'/home/me/keys/id ed25519'") {
		t.Errorf("git env %q must quote the key path", env)
	}
	if !strings.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("git env %q must disable prompts", env)
	}
}

func TestListFiltersByBaseApp(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"apps:list": {out: `["api","api-main","api-feat-login","web-main"]`},
	}}
	cl, _ := New(testTarget(), "", "")
	cl.Run = f.run

	apps, err := cl.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api-feat-login", "api-main"}
	if len(apps) != len(want) {
		t.Fatalf("got %v, want %v", apps, want)
	}
	for i := range want {
		if apps[i] != want[i] {
			t.Fatalf("got %v, want %v", apps, want)
		}
	}
}

func TestListFallsBackToPlainOutput(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"apps:list": {out: "=====> My Apps\napi\napi-main\nweb\n"},
	}}
	cl, _ := New(testTarget(), "", "")
	cl.Run = f.run

	apps, err := cl.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0] != "api-main" {
		t.Fatalf("got %v, want [api-main]", apps)
	}
}

func TestCurrentBranchRejectsDetachedHead(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{"rev-parse": {out: "HEAD\n"}}}
	if _, err := CurrentBranch(context.Background(), f.run, "/repo"); err == nil {
		t.Error("expected an error on a detached HEAD")
	}
	f2 := &fakeRunner{replies: map[string]reply{"rev-parse": {out: "feat/x\n"}}}
	branch, err := CurrentBranch(context.Background(), f2.run, "/repo")
	if err != nil || branch != "feat/x" {
		t.Errorf("got %q, %v", branch, err)
	}
}
