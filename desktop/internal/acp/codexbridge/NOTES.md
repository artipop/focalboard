# codex exec --json protocol notes

Codex has **no ACP mode** (no `codex acp` subcommand). This bridge drives
`codex exec --json` — one non-interactive turn per prompt — and translates its
NDJSON event stream into ACP session updates.

## Invocation

```
codex exec --json --skip-git-repo-check -C <cwd> [-m <model>] [args…] <prompt>
```

- Prompt is passed as the final positional argument; stdin is closed.
- `--skip-git-repo-check` lets sessions run in non-git worktrees too.
- Approval/sandbox is set via `args` (e.g. `--sandbox workspace-write`,
  `--ask-for-approval never`), not via interactive per-tool round-trips — codex
  exec is non-interactive.
- Per-agent token isolation is achieved with env, not flags: give each agent its
  own `CODEX_HOME` (a dir with its own `auth.json`) or `OPENAI_API_KEY`, injected
  through `procgroup.Spawn`'s `extraEnv`/`dropEnv`.

## Event stream (top-level `type`)

```
{"type":"thread.started","thread_id":"…"}
{"type":"turn.started"}
{"type":"item.started","item":{…}}
{"type":"item.updated","item":{…}}
{"type":"item.completed","item":{…}}
{"type":"turn.completed","usage":{…}}
```

Failure surfaces as `turn.failed` / `error` (with an `error.message`).

## Item kinds (`item.type`)

- `agent_message` `{id, text}` — assistant output. Emitted as `UpdateAgentMessageText`
  on `item.completed`; the session layer accumulates it into the final card comment.
- `reasoning` `{id, text}` — thinking. Emitted as `UpdateAgentThoughtText`.
- `command_execution` `{id, command, aggregated_output, exit_code, status}` — a
  shell command. Surfaced as an ACP tool call (`StartToolCall` on start,
  `UpdateToolCall` completed/failed based on `status`/`exit_code`).
- other tool-like items (`file_change`, `mcp_tool_call`, …) — surfaced as generic
  tool calls keyed by `item.id`.

Observed against the codex CLI installed on this machine. Sample captures:

```
{"type":"thread.started","thread_id":"019f91af-…"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"DONE"}}
{"type":"turn.completed","usage":{"input_tokens":11103,…}}
```

```
{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"/bin/zsh -lc 'cat sample.txt'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"…","aggregated_output":"hello\nworld\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"`sample.txt` contains 2 lines."}}
```

## Not yet handled / future work

- No incremental text deltas: codex emits whole `agent_message` text on
  `item.completed` (no token streaming in this schema). If a streaming variant
  appears, handle it in `item.updated`.
- Tool-call detail (diffs, file paths, command output) is not forwarded beyond
  status + title; enrich via `WithUpdateRawInput`/content if needed.
