package acp

import "strings"

// ToolPolicy is the list of tool calls a session may make without asking the
// user. An entry is either a bare tool name — "Read", meaning any call to it —
// or a name with an argument pattern — "Bash(git log*)" — which matches only
// calls whose principal argument fits the pattern.
//
// The distinction matters because the interesting tools are not uniform: Read
// is as safe as its name whatever the path, while Bash covers both `git status`
// and `rm -rf /`. Allowing Bash wholesale is the difference between an agent
// that can look around and one that can do anything, so a policy that cannot
// say "only these commands" ends up either uselessly tight or far too loose.
type ToolPolicy []string

// patternArg names the input field a pattern is matched against, per tool. Only
// tools that take one meaningful argument can be narrowed this way.
var patternArg = map[string]string{
	"Bash": "command",
}

// readOnlyShellCommands are the commands a planning session may run unasked.
// Exploring a repository is most of what planning does, and forbidding the
// shell outright pushes the agent into declaring it cannot see the code at all
// — so the useful read-only commands are listed instead, and everything else
// asks. Nothing here writes, and notably absent is `find`, which can delete
// through -delete and -exec.
var readOnlyShellCommands = []string{
	"ls", "cat", "head", "tail", "wc", "file", "tree", "pwd",
	"rg", "grep",
	"git log", "git show", "git status", "git diff", "git branch", "git ls-files",
}

// defaultPlanningTools is the built-in read-only policy for planning sessions.
func defaultPlanningTools() []string {
	tools := []string{"Read", "Grep", "Glob"}
	for _, cmd := range readOnlyShellCommands {
		// Both forms are listed on purpose: a lone "ls*" would also match
		// "lsof", so the bare command and its argument form are spelled out.
		tools = append(tools, "Bash("+cmd+")", "Bash("+cmd+" *)")
	}
	return tools
}

// Allows reports whether the call runs unasked. input is the tool's raw input
// as the agent sent it; a nil input can only satisfy bare-name entries.
func (p ToolPolicy) Allows(tool string, input any) bool {
	for _, entry := range p {
		name, pattern, hasPattern := splitPolicyEntry(entry)
		if !strings.EqualFold(name, tool) {
			continue
		}
		if !hasPattern {
			return true
		}
		arg, ok := policyArg(tool, input)
		if !ok {
			// The entry is narrower than what we can check, so it cannot be
			// used to approve this call. Something else may still allow it.
			continue
		}
		if matchPattern(pattern, arg) {
			return true
		}
	}
	return false
}

// splitPolicyEntry parses "Bash(git log*)" into its name and pattern.
func splitPolicyEntry(entry string) (name, pattern string, hasPattern bool) {
	entry = strings.TrimSpace(entry)
	open := strings.Index(entry, "(")
	if open == -1 || !strings.HasSuffix(entry, ")") {
		return entry, "", false
	}
	return strings.TrimSpace(entry[:open]), entry[open+1 : len(entry)-1], true
}

// policyArg pulls the argument a pattern applies to out of the tool input.
func policyArg(tool string, input any) (string, bool) {
	field, ok := patternArg[canonicalToolName(tool)]
	if !ok {
		return "", false
	}
	fields, ok := input.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := fields[field].(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// canonicalToolName maps a tool to the spelling patternArg is keyed by, so a
// policy written as "bash(...)" still narrows the Bash tool.
func canonicalToolName(tool string) string {
	for known := range patternArg {
		if strings.EqualFold(known, tool) {
			return known
		}
	}
	return tool
}

// matchPattern is a deliberately small glob: "*" stands for any run of
// characters and everything else is literal. Commands are not paths, so the
// path-aware matchers in the standard library would treat "/" as a boundary
// and refuse to match `git log -- src/foo`.
func matchPattern(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		idx := strings.Index(s, part)
		if idx == -1 {
			return false
		}
		s = s[idx+len(part):]
	}
	return strings.HasSuffix(s, last) && len(s) >= len(last)
}

// agentPolicy returns the agent's own tool policy, or nil to fall back to the
// global one. Resolved at session start so a registry edit mid-session cannot
// widen what a running agent may do.
func agentPolicy(a AgentEntry) ToolPolicy {
	if len(a.AutoAllowTools) == 0 {
		return nil
	}
	return ToolPolicy(a.AutoAllowTools)
}
