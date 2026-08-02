package acp

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// A session can offer its agent extra tools through MCP servers. There are two
// — the dokku deploy server and the webtest browser server — and both are our
// own binary re-invoked as `<self> mcp <name>`, configured entirely through
// their environment, so the model picks steps but never targets.
//
// Every agent takes the same description by the same road: session/new, where
// the protocol has a field for them. That is one of the things the vendor
// adapters bought — the CLI-specific ways of declaring a server (a --mcp-config
// flag, a set of -c overrides) are gone along with the bridges that needed them.

// builtinMCPNames are the servers a session spawns itself, with per-session
// configuration the agent must not be able to supply: the deploy target, the
// repository and the branch arrive in the dokku server's environment, which is
// what leaves the model choosing the branch and nothing else. An agent's own
// entry may not take one of these names.
var builtinMCPNames = []string{dokku.ServerName}

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
				dokku.EnvTarget:    string(target),
				dokku.EnvRepo:      s.RepoPath,
				dokku.EnvBranch:    s.DeployBranch,
				dokku.EnvArtifacts: s.Artifacts,
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
	// Sorted, because a map has no order and the agent's command line should
	// not change between two runs of the same configuration.
	names := make([]string, 0, len(s.Agent.MCPServers))
	for name := range s.Agent.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]mcpServerSpec, 0, len(names))
	for _, name := range names {
		srv := s.Agent.MCPServers[name]
		specs = append(specs, mcpServerSpec{
			Name:    name,
			Command: srv.Command,
			Args:    append([]string(nil), srv.Args...),
			Env:     srv.Env,
		})
		s.allowToolPrefix("mcp__" + name + "__")
	}
	return specs
}

// acpMCPServers renders the servers for session/new.
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

// sortedEnvNames keeps generated argv stable between runs (map order is not).
func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
