package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Repo registry: named local repositories, edited from the desktop UI and
// persisted back into the config file. Cards are mapped to a repo when one of
// their select/multiSelect option names (e.g. a tag) matches an entry name.

// Repos returns a snapshot of the registry.
func (m *Manager) Repos() []RepoEntry {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return append([]RepoEntry(nil), m.cfg.Repos...)
}

// AddRepo registers a local git repository under name (defaults to the
// directory basename) and persists the config.
//
// TODO: validate the name as a hostname label. A deploy target names its apps
// and its subdomain after the repository (dokku.AppLabel), so a name that is not
// a valid DNS label is folded there silently, and two names that fold together
// would share one subdomain. The check belongs here, where the name is typed.
//
// TODO: this entry is also where a preview's own settings belong — config
// variables for the branch app, whether to request a Let's Encrypt certificate,
// how long a build may take. They were on the deploy target, which is wrong: a
// target is a machine, and those describe the thing being deployed.
func (m *Manager) AddRepo(name, path string) (RepoEntry, error) {
	if !filepath.IsAbs(path) {
		return RepoEntry{}, fmt.Errorf("путь должен быть абсолютным: %s", path)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return RepoEntry{}, fmt.Errorf("каталог не найден: %s", clean)
	}
	if _, err := gitCmd(context.Background(), clean, "rev-parse", "--git-dir"); err != nil {
		return RepoEntry{}, fmt.Errorf("%s не является git-репозиторием", clean)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(clean)
	}

	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for _, r := range m.cfg.Repos {
		if strings.EqualFold(r.Name, name) {
			return RepoEntry{}, fmt.Errorf("имя %q уже занято (%s)", r.Name, r.Path)
		}
		if filepath.Clean(r.Path) == clean {
			return RepoEntry{}, fmt.Errorf("репозиторий уже добавлен как %q", r.Name)
		}
	}
	entry := RepoEntry{Name: name, Path: clean}
	m.cfg.Repos = append(m.cfg.Repos, entry)
	return entry, m.persistConfigLocked()
}

// RemoveRepo deletes a registry entry by name and persists the config.
func (m *Manager) RemoveRepo(name string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for i, r := range m.cfg.Repos {
		if strings.EqualFold(r.Name, name) {
			m.cfg.Repos = append(m.cfg.Repos[:i], m.cfg.Repos[i+1:]...)
			return m.persistConfigLocked()
		}
	}
	return fmt.Errorf("репозиторий %q не найден", name)
}

func (m *Manager) persistConfigLocked() error {
	if m.cfgPath == "" {
		return nil // tests / ephemeral configs
	}
	if err := SaveConfig(m.cfgPath, m.cfg); err != nil {
		m.log.Error("acp: failed to persist config", "err", err)
		return fmt.Errorf("не удалось сохранить конфиг: %w", err)
	}
	return nil
}

// resolveRepo maps a trigger event to a repository path. Priority:
//  1. explicit repo_path card property (validated against whitelist+registry);
//  2. a select/multiSelect option name (tag) matching a registry entry;
//  3. the name of the column the card was dragged out of — supports boards
//     whose trigger property has one lane per repository.
//
// resolveNamedRepo looks a registry entry up by name. Opening a console on a
// card that carries no repository tag would otherwise be a dead end: the card
// is not going to grow one just because someone wants to talk about it.
func (m *Manager) resolveNamedRepo(name string) (string, error) {
	m.cfgMu.RLock()
	repos := append([]RepoEntry(nil), m.cfg.Repos...)
	m.cfgMu.RUnlock()
	for _, r := range repos {
		if strings.EqualFold(strings.TrimSpace(name), r.Name) {
			if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
				return "", fmt.Errorf("репозиторий %q указывает на несуществующий каталог %s", r.Name, r.Path)
			}
			return r.Path, nil
		}
	}
	return "", fmt.Errorf("репозиторий %q не найден в реестре (%s)", name, repoNames(repos))
}

func (m *Manager) resolveRepo(ev CardMoved) (string, error) {
	if explicit := strings.TrimSpace(ev.Props["repo_path"]); explicit != "" {
		m.cfgMu.RLock()
		cfg := m.cfg
		m.cfgMu.RUnlock()
		return cfg.ValidateRepoPath(explicit)
	}

	m.cfgMu.RLock()
	repos := append([]RepoEntry(nil), m.cfg.Repos...)
	m.cfgMu.RUnlock()

	candidates := append(append([]string(nil), ev.OptionNames...), ev.FromColumn.Name)
	for _, opt := range candidates {
		for _, r := range repos {
			if strings.EqualFold(strings.TrimSpace(opt), r.Name) {
				if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
					return "", fmt.Errorf("репозиторий %q указывает на несуществующий каталог %s", r.Name, r.Path)
				}
				return r.Path, nil
			}
		}
	}
	if len(repos) == 0 {
		return "", fmt.Errorf("не задан ни repo_path на карточке, ни репозитории в реестре (меню доски → Agent repositories)")
	}
	return "", fmt.Errorf("ни тег карточки, ни исходная колонка не совпали с репозиторием из реестра (%s)", repoNames(repos))
}

func repoNames(repos []RepoEntry) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}
