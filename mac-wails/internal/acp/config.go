package acp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RepoEntry is one named local repository in the registry.
type RepoEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Config controls the agent integration. It is stored as JSON in the app data
// directory; the repo registry is edited through the desktop UI, the rest by
// hand for now.
type Config struct {
	Enabled bool `json:"enabled"`

	// AgentMode selects how sessions run: "claude" (built-in bridge spawning
	// the claude binary) or "acp-command" (arbitrary external ACP agent).
	AgentMode string `json:"agentMode"`
	// ClaudePath overrides claude binary discovery for agentMode "claude".
	ClaudePath string `json:"claudePath,omitempty"`
	// AgentCommand is the argv of an external ACP agent for agentMode "acp-command".
	AgentCommand []string `json:"agentCommand,omitempty"`

	TriggerProperty string `json:"triggerProperty"`
	TriggerColumn   string `json:"triggerColumn"`

	// RepoWhitelist lists directory roots a card's repo_path must be under.
	// Empty means every repo_path is rejected (explicit opt-in).
	RepoWhitelist []string `json:"repoWhitelist"`

	// Repos is the registry of named local repositories. A card is mapped to
	// a repo when one of its select/multiSelect option names (e.g. a tag)
	// matches a registry entry name. Registered paths are implicitly allowed.
	Repos []RepoEntry `json:"repos"`

	MaxConcurrent            int      `json:"maxConcurrent"`
	SessionTimeoutMinutes    int      `json:"sessionTimeoutMinutes"`
	PermissionTimeoutMinutes int      `json:"permissionTimeoutMinutes"`
	IdempotencyWindowSeconds int      `json:"idempotencyWindowSeconds"`
	AutoAllowTools           []string `json:"autoAllowTools"`
	ShowThoughts             bool     `json:"showThoughts"`
	WorktreeDir              string   `json:"worktreeDir"`
	KeepFailedWorktrees      bool     `json:"keepFailedWorktrees"`
}

// DefaultConfig returns the defaults written on first run. dataDir is the ACP
// data directory (worktrees live under it).
func DefaultConfig(dataDir string) Config {
	return Config{
		Enabled:                  true,
		AgentMode:                "claude",
		TriggerProperty:          "Status",
		TriggerColumn:            "To Agent",
		RepoWhitelist:            []string{},
		Repos:                    []RepoEntry{},
		MaxConcurrent:            3,
		SessionTimeoutMinutes:    15,
		PermissionTimeoutMinutes: 5,
		IdempotencyWindowSeconds: 10,
		AutoAllowTools:           []string{"Read", "Grep", "Glob", "Edit", "Write", "MultiEdit", "NotebookEdit", "TodoWrite"},
		WorktreeDir:              filepath.Join(dataDir, "worktrees"),
	}
}

// LoadConfig reads path, creating it with defaults when absent.
func LoadConfig(path, dataDir string) (Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig(dataDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, err
		}
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig(dataDir)
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) SessionTimeout() time.Duration {
	return time.Duration(c.SessionTimeoutMinutes) * time.Minute
}

func (c Config) PermissionTimeout() time.Duration {
	return time.Duration(c.PermissionTimeoutMinutes) * time.Minute
}

func (c Config) IdempotencyWindow() time.Duration {
	return time.Duration(c.IdempotencyWindowSeconds) * time.Second
}

// ToolAllowed reports whether toolName is on the auto-allow list.
func (c Config) ToolAllowed(toolName string) bool {
	for _, t := range c.AutoAllowTools {
		if strings.EqualFold(t, toolName) {
			return true
		}
	}
	return false
}

// SaveConfig writes cfg to path (used when the UI edits the repo registry).
func SaveConfig(path string, cfg Config) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// ValidateRepoPath checks a card's repo_path against the whitelist, the repo
// registry and the filesystem. It returns the cleaned absolute path.
func (c Config) ValidateRepoPath(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", fmt.Errorf("repo_path is empty")
	}
	if !filepath.IsAbs(repoPath) {
		return "", fmt.Errorf("repo_path must be absolute: %s", repoPath)
	}
	clean := filepath.Clean(repoPath)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("repo_path does not exist: %s", clean)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_path is not a directory: %s", clean)
	}
	roots := append([]string(nil), c.RepoWhitelist...)
	for _, r := range c.Repos {
		roots = append(roots, r.Path)
	}
	for _, root := range roots {
		rootClean := filepath.Clean(root)
		if clean == rootClean || strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("repo_path %s is not under any whitelisted root (repoWhitelist / repos in acp config)", clean)
}
