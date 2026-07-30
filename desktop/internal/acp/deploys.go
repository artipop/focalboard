package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// Deploy target registry: named Dokku destinations, edited from the desktop UI
// and persisted into the config file. A card moved into the deploy column is
// matched to one of these, and the entry is what the session's MCP server is
// configured from.

// Deploys returns a snapshot of the registry.
func (m *Manager) Deploys() []DeployEntry {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return append([]DeployEntry(nil), m.cfg.Deploys...)
}

// validateDeploy normalizes and checks a registry entry.
func validateDeploy(d DeployEntry) (DeployEntry, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return DeployEntry{}, fmt.Errorf("имя цели не может быть пустым")
	}
	target, err := d.Target.Validate()
	if err != nil {
		return DeployEntry{}, fmt.Errorf("цель %q: %w", d.Name, err)
	}
	d.Target = target

	if key := strings.TrimSpace(d.SSHKey); key != "" {
		if _, err := os.Stat(key); err != nil {
			return DeployEntry{}, fmt.Errorf("ssh-ключ не найден: %s", key)
		}
	}
	return d, nil
}

// AddDeploy registers a new Dokku destination and persists the config.
func (m *Manager) AddDeploy(d DeployEntry) (DeployEntry, error) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	d, err := validateDeploy(d)
	if err != nil {
		return DeployEntry{}, err
	}
	for _, e := range m.cfg.Deploys {
		if strings.EqualFold(e.Name, d.Name) {
			return DeployEntry{}, fmt.Errorf("цель с именем %q уже существует", e.Name)
		}
	}
	m.cfg.Deploys = append(m.cfg.Deploys, d)
	return d, m.persistConfigLocked()
}

// UpdateDeploy replaces an existing entry (matched by name) and persists.
func (m *Manager) UpdateDeploy(d DeployEntry) (DeployEntry, error) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	d, err := validateDeploy(d)
	if err != nil {
		return DeployEntry{}, err
	}
	for i, e := range m.cfg.Deploys {
		if strings.EqualFold(e.Name, d.Name) {
			m.cfg.Deploys[i] = d
			return d, m.persistConfigLocked()
		}
	}
	return DeployEntry{}, fmt.Errorf("цель %q не найдена", d.Name)
}

// RemoveDeploy deletes an entry by name and persists the config. Apps already
// running on the host are left alone — they are removed from the agent console.
func (m *Manager) RemoveDeploy(name string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for i, e := range m.cfg.Deploys {
		if strings.EqualFold(e.Name, name) {
			m.cfg.Deploys = append(m.cfg.Deploys[:i], m.cfg.Deploys[i+1:]...)
			return m.persistConfigLocked()
		}
	}
	return fmt.Errorf("цель %q не найдена", name)
}

// resolveDeployTarget maps a card to a Dokku destination: a select/multiSelect
// option naming an entry, otherwise the single registered entry. A target is a
// host rather than a per-repository setting, so one entry usually answers for
// everything and a card names one only where there are several hosts.
func (m *Manager) resolveDeployTarget(ev CardMoved) (DeployEntry, error) {
	m.cfgMu.RLock()
	deploys := append([]DeployEntry(nil), m.cfg.Deploys...)
	m.cfgMu.RUnlock()

	if len(deploys) == 0 {
		return DeployEntry{}, fmt.Errorf("не настроено ни одной цели деплоя (меню доски → Deploy targets)")
	}
	for _, opt := range ev.OptionNames {
		for _, d := range deploys {
			if strings.EqualFold(strings.TrimSpace(opt), d.Name) {
				return d, nil
			}
		}
	}
	if len(deploys) == 1 {
		return deploys[0], nil
	}
	return DeployEntry{}, fmt.Errorf("не понятно, куда деплоить: тег карточки не совпал ни с одной целью из реестра (%s)", deployNames(deploys))
}

// resolveDeploy gathers what a deploy session needs: the target and the branch
// to publish. For an ordinary session it returns nothing and no error, so the
// launch path can call it unconditionally.
func (m *Manager) resolveDeploy(ev CardMoved, repoPath string, deploy bool) (*DeployEntry, string, error) {
	if !deploy {
		return nil, "", nil
	}
	target, err := m.resolveDeployTarget(ev)
	if err != nil {
		return nil, "", err
	}
	target.Target = target.Target.WithBaseApp(m.deployAppName(repoPath))
	branch, err := resolveDeployBranch(ev, repoPath)
	if err != nil {
		return nil, "", err
	}
	return &target, branch, nil
}

// deployAppName is what a target without an explicit base app names its apps
// and its level of the hostname after: the repository's own name in the
// registry, or the directory it sits in for a repository that is not registered.
func (m *Manager) deployAppName(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	m.cfgMu.RLock()
	repos := append([]RepoEntry(nil), m.cfg.Repos...)
	m.cfgMu.RUnlock()

	if name := repoNameForPath(repos, repoPath); name != "" {
		return name
	}
	return filepath.Base(filepath.Clean(repoPath))
}

// resolveDeployBranch is the branch a deploy session publishes: the card's
// explicit "branch" property, otherwise whatever the repository has checked out.
func resolveDeployBranch(ev CardMoved, repoPath string) (string, error) {
	if b := strings.TrimSpace(ev.Props["branch"]); b != "" {
		return b, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return dokku.CurrentBranch(ctx, nil, repoPath)
}

func repoNameForPath(repos []RepoEntry, path string) string {
	for _, r := range repos {
		if r.Path == path {
			return r.Name
		}
	}
	return ""
}

func deployNames(deploys []DeployEntry) string {
	names := make([]string, len(deploys))
	for i, d := range deploys {
		names[i] = d.Name
	}
	return strings.Join(names, ", ")
}
