package acp

import (
	"fmt"
	"strings"
)

// Some agents are an adapter in front of a CLI, and the CLI can do things the
// adapter has no way to mention: Claude's Remote Control (driving this session
// from claude.ai or the phone) is a flag of the CLI and nothing in ACP, so the
// capability probe cannot find it and never will.
//
// The adapter does provide a door, and documents it in its own types: session/new
// takes `_meta.claudeCode.options` — the Claude Agent SDK's Options — and passes
// `extraArgs` through to the CLI it spawns. That is the whole mechanism here: an
// agent may carry extra CLI arguments, and they are handed over in `_meta` under
// the namespace its adapter reads.
//
// It is deliberately a hand-typed field rather than a list of features of ours.
// We do not know what the CLI takes, and it is not our business to keep such a
// list in step with somebody else's releases; what we owe the user is that the
// arguments arrive and that a mistake is loud. Both hold: an argument the CLI
// does not know fails session/new with the CLI's own message ("unknown option
// '--nonsense'"), so a typo is seen when the agent is saved, not an hour later
// on a card.

// sdkOptionsMeta is the `_meta` namespace an adapter reads its CLI options from,
// per kind. Only claude has one — codex drives its CLI over that CLI's own
// JSON-RPC, and the ACP-native kinds are the CLI, so their arguments already go
// on the command line (AgentEntry.Args).
var sdkOptionsMeta = map[string]string{
	AgentKindClaude: "claudeCode",
}

// cliHandoffKind reports the namespace CLI arguments are handed over in, and
// whether the kind has one at all.
func cliHandoffKind(kind string) (string, bool) {
	ns, ok := sdkOptionsMeta[kind]
	return ns, ok
}

// sessionMeta is the `_meta` of session/new for this agent: the CLI arguments
// it carries, in the shape the adapter reads them. Nil when there are none,
// which is every agent that has not been given any.
func sessionMeta(a AgentEntry) map[string]any {
	if len(a.CLIArgs) == 0 {
		return nil
	}
	ns, ok := cliHandoffKind(a.Kind)
	if !ok {
		// Validation refuses this combination, so reaching here means a
		// hand-edited config: the arguments would be silently dropped, and
		// dropping them quietly is what we are trying not to do — but a session
		// is the wrong place to fail over a setting, so they are left out and
		// the config is what has to be fixed.
		return nil
	}
	return map[string]any{
		ns: map[string]any{
			"options": map[string]any{"extraArgs": extraArgs(a.CLIArgs)},
		},
	}
}

// extraArgs converts an argv into the map the SDK takes: a flag mapped to its
// value, or to "" when it is a switch. The dashes are dropped because that is
// how the SDK spells them — it puts them back on.
//
//	--remote-control                        → {"remote-control": ""}
//	--remote-control-session-name-prefix x  → {"remote-control-session-name-prefix": "x"}
//	--fallback-model=sonnet                 → {"fallback-model": "sonnet"}
func extraArgs(argv []string) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(argv); i++ {
		arg := strings.TrimSpace(argv[i])
		if arg == "" || !strings.HasPrefix(arg, "-") {
			continue // a value already taken by the flag before it
		}
		name := strings.TrimLeft(arg, "-")
		if name, value, ok := strings.Cut(name, "="); ok {
			out[name] = value
			continue
		}
		// The next element is this flag's value only if it is not a flag itself.
		if i+1 < len(argv) && !strings.HasPrefix(strings.TrimSpace(argv[i+1]), "-") {
			out[name] = strings.TrimSpace(argv[i+1])
			i++
			continue
		}
		out[name] = ""
	}
	return out
}

// validateCLIArgs normalizes the arguments and refuses them for a kind that has
// nowhere to put them, rather than accepting a setting that would do nothing.
func validateCLIArgs(a AgentEntry) ([]string, error) {
	args := a.CLIArgs[:0:0]
	for _, arg := range a.CLIArgs {
		if arg = strings.TrimSpace(arg); arg != "" {
			args = append(args, arg)
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	if _, ok := cliHandoffKind(a.Kind); !ok {
		return nil, fmt.Errorf("агенту типа %q нельзя передать аргументы CLI: у его адаптера нет такого канала — используйте «Дополнительные аргументы» (они уходят в саму команду запуска)", a.Kind)
	}
	if !strings.HasPrefix(args[0], "-") {
		return nil, fmt.Errorf("аргументы CLI должны начинаться с флага, например --remote-control (получено %q)", args[0])
	}
	return args, nil
}
