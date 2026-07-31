package acp

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// A session can offer its agent extra tools through MCP servers. There are two
// — the dokku deploy server and the webtest browser server — and both are our
// own binary re-invoked as `<self> mcp <name>`, configured entirely through
// their environment, so the model picks steps but never targets.
//
// Every agent kind takes the same description by a different road: claude gets
// --mcp-config, codex gets -c overrides, and an ACP-native agent gets the
// servers in session/new, where the protocol has a field for them.

// mcpServerSpec is one stdio MCP server offered to an agent.
type mcpServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// sessionMCPServers returns the MCP servers a session runs with: the deploy
// server we spawn ourselves for a deploy column, plus whatever the agent
// carries of its own — which is what a test session drives the browser with.
func sessionMCPServers(s *Session, _ Config) ([]mcpServerSpec, error) {
	// The agent's own servers travel with every session it runs, whatever the
	// column started it: they are part of how that agent works, not of what
	// this particular card is for.
	specs := agentMCPServers(s)
	if s.Deploy != nil {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("не удалось определить путь к приложению для MCP-сервера: %w", err)
		}
		target, err := json.Marshal(s.Deploy.Target)
		if err != nil {
			return nil, fmt.Errorf("не удалось сериализовать цель деплоя: %w", err)
		}
		specs = append([]mcpServerSpec{{
			Name:    dokku.ServerName,
			Command: self,
			Args:    []string{"mcp", dokku.ServerName},
			Env: map[string]string{
				dokku.EnvTarget: string(target),
				dokku.EnvRepo:   s.RepoPath,
				dokku.EnvBranch: s.DeployBranch,
			},
		}}, specs...)
	}
	if len(specs) > 0 {
		// Whoever configured a server — us or the user — consented to it being
		// launched, so the agent's "may I start MCP?" prompt is ours to answer.
		s.markMCPConfigured()
	}
	return specs, nil
}

// agentMCPServers turns the agent's registry entries into specs and records
// their tool prefixes on the session: wiring a server to an agent is consent to
// use it, and a card-triggered session has no console to ask.
func agentMCPServers(s *Session) []mcpServerSpec {
	if len(s.Agent.MCPServers) == 0 {
		return nil
	}
	specs := make([]mcpServerSpec, 0, len(s.Agent.MCPServers))
	for _, srv := range s.Agent.MCPServers {
		specs = append(specs, mcpServerSpec{
			Name:    srv.Name,
			Command: srv.Command[0],
			Args:    append([]string(nil), srv.Command[1:]...),
			Env:     srv.Env,
		})
		s.allowToolPrefix("mcp__" + srv.Name + "__")
	}
	return specs
}

func claudeMCPArgs(specs []mcpServerSpec) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	type serverJSON struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}
	servers := make(map[string]serverJSON, len(specs))
	for _, s := range specs {
		servers[s.Name] = serverJSON{Command: s.Command, Args: s.Args, Env: s.Env}
	}
	cfg, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return nil, err
	}
	return []string{"--mcp-config", string(cfg)}, nil
}

// codexMCPArgs renders the servers as codex config overrides. Codex has no
// --mcp-config: `-c key=value` sets one config key per flag, the value being
// TOML — hence dotted keys rather than one inline table, which keeps the
// escaping to individual strings.
func codexMCPArgs(specs []mcpServerSpec) []string {
	var args []string
	for _, s := range specs {
		key := "mcp_servers." + s.Name
		args = append(args, "-c", key+".command="+tomlString(s.Command))
		if len(s.Args) > 0 {
			quoted := make([]string, len(s.Args))
			for i, a := range s.Args {
				quoted[i] = tomlString(a)
			}
			args = append(args, "-c", key+".args=["+strings.Join(quoted, ", ")+"]")
		}
		for _, name := range sortedEnvNames(s.Env) {
			args = append(args, "-c", key+".env."+name+"="+tomlString(s.Env[name]))
		}
	}
	return args
}

// acpMCPServers renders the servers for session/new of an ACP-native agent.
func acpMCPServers(specs []mcpServerSpec) []acpsdk.McpServer {
	servers := make([]acpsdk.McpServer, 0, len(specs))
	for _, s := range specs {
		env := make([]acpsdk.EnvVariable, 0, len(s.Env))
		for _, name := range sortedEnvNames(s.Env) {
			env = append(env, acpsdk.EnvVariable{Name: name, Value: s.Env[name]})
		}
		servers = append(servers, acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     env,
		}})
	}
	return servers
}

// tomlString quotes a value for a TOML basic string. Go's own quoting escapes
// the same characters TOML does, and leaves printable non-ASCII alone, which
// TOML allows.
func tomlString(s string) string {
	return strconv.Quote(s)
}

// sortedEnvNames keeps generated argv stable between runs (map order is not).
func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
