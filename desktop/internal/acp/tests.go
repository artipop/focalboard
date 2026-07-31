package acp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// The test column points a session at a running preview instead of at the
// repository. Everything it needs is derived from what the deploy column
// already produced, so a card that was deployed is testable without any extra
// configuration.

// TestRun is what a test session was pointed at: the address under test and the
// directory its evidence lands in.
type TestRun struct {
	URL       string
	Branch    string // branch behind the preview, when it is known
	Artifacts string
}

// resolveTestRun gathers what a test session needs. For any other session it
// returns nothing and no error, so the launch path can call it unconditionally.
func (m *Manager) resolveTestRun(ev CardMoved, repoPath, sessionID string, test bool) (*TestRun, error) {
	if !test {
		return nil, nil
	}
	previewURL, branch, err := m.resolvePreviewURL(ev, repoPath)
	if err != nil {
		return nil, err
	}
	m.cfgMu.RLock()
	root := m.cfg.ArtifactsDir
	m.cfgMu.RUnlock()

	run := &TestRun{URL: previewURL, Branch: branch}
	if strings.TrimSpace(root) != "" {
		run.Artifacts = filepath.Join(root, sessionID)
		if err := os.MkdirAll(run.Artifacts, 0o755); err != nil {
			return nil, fmt.Errorf("не удалось создать каталог артефактов: %w", err)
		}
	}
	return run, nil
}

// resolvePreviewURL is where the card's preview lives. Priority:
//  1. an explicit preview_url property on the card — whatever put the link
//     there (the deploy session, a person, CI) wins;
//  2. the address the deploy registry would give the card's branch, which is
//     deterministic: one branch, one app, one subdomain.
func (m *Manager) resolvePreviewURL(ev CardMoved, repoPath string) (string, string, error) {
	if raw := cardPreviewURL(ev); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.Host == "" {
			return "", "", fmt.Errorf("свойство preview_url карточки (%q) не похоже на адрес — нужен полный URL вида https://feature-x.example.com", raw)
		}
		return u.String(), strings.TrimSpace(ev.Props["branch"]), nil
	}

	target, err := m.resolveDeployTarget(ev)
	if err != nil {
		return "", "", fmt.Errorf("не удалось понять, что тестировать: у карточки нет свойства preview_url, и %w", err)
	}
	// The address has to be computed exactly as the deploy computes it, app
	// name included — that is what makes "deployed, then tested" need no
	// configuration of its own.
	target.Target = target.Target.WithBaseApp(m.deployAppName(repoPath))
	branch, err := resolveDeployBranch(ev, repoPath)
	if err != nil {
		return "", "", fmt.Errorf("не удалось определить ветку карточки для адреса превью: %w", err)
	}
	return target.URL(dokku.AppSlug(branch)), branch, nil
}

// cardPreviewURL reads the card's preview link, accepting the property named
// either way round: boardadapter lowercases property names, and a board is as
// likely to call the field "Preview URL" as "preview_url".
func cardPreviewURL(ev CardMoved) string {
	for _, key := range []string{"preview_url", "preview url"} {
		if v := strings.TrimSpace(ev.Props[key]); v != "" {
			return v
		}
	}
	return ""
}

// composeTestPrompt builds the task text of a test session: the same system
// prompts every session gets, then the tester instructions, then the concrete
// facts — what to open and what the card asked for, which is the scenario.
func composeTestPrompt(ev CardMoved, agent AgentEntry, systemPrompt, testPrompt string, run TestRun) string {
	var b []byte
	if p := strings.TrimSpace(systemPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(agent.Prompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(testPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	} else {
		b = fmt.Appendf(b, "%s\n\n", DefaultTestPrompt)
	}
	b = fmt.Appendf(b, "Карточка: %s\nАдрес превью: %s\n", ev.Title, run.URL)
	if run.Branch != "" {
		b = fmt.Appendf(b, "Ветка: %s\n", run.Branch)
	}
	if run.Artifacts != "" {
		// The report is a file now, so the agent has to be told where to put it
		// and its evidence.
		b = fmt.Appendf(b, "Отчёт: %s\nСкриншоты складывай в: %s\n",
			filepath.Join(run.Artifacts, ResultFile), filepath.Join(run.Artifacts, ScreenshotDir))
	}
	if ev.Body != "" {
		b = fmt.Appendf(b, "\nЧто должно работать (описание карточки):\n%s\n", ev.Body)
	} else {
		b = fmt.Appendf(b, "\nОписания у карточки нет — пройди основные сценарии приложения и проверь, что ничего не сломано.\n")
	}
	return string(b)
}
