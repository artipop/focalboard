# Фаза 0 — результаты спайка (2026-07-23)

Проверено на: `claude` 2.1.218 (нативный бинарь, `~/.local/bin/claude`), Go 1.26.5,
`github.com/coder/acp-go-sdk v0.13.5`, `github.com/beyond5959/acp-adapter v0.3.8`.

## Вердикт: пишем свой мост (`internal/acp/claudebridge`)

`acp-adapter/pkg/claudeacp` (режим `-mode lib`) прошёл полный ACP-цикл in-process
(`io.Pipe`, без Node и без subprocess-адаптера), но для наших требований — тупик:

- **Не проксирует разрешения.** Спавнит `claude -p <prompt>` на каждый ход с
  `--dangerously-skip-permissions` (default) либо статичным `--allowedTools`.
  Модалка разрешений (ТЗ §5.1, приёмка §10.5) невозможна.
- Грубая трансляция событий: lifecycle-маркеры (`turn_started`, `item_started`)
  приходят как `agent_thought_chunk`, отдельных `tool_call`/`tool_call_update`
  нотификаций нет — панель выполнения (ТЗ §4.3) не наполнить.
- Один subprocess на каждый ход (`--resume` между ходами) — дороже и хрупче.

Свой мост работает поверх нативного stream-json протокола `claude` — проверен
в `-mode raw`, все нужные примитивы есть (см. ниже).

## Зафиксированный протокол `claude` stream-json (режим raw)

Запуск: `claude --input-format stream-json --output-format stream-json --verbose
--include-partial-messages --permission-prompt-tool stdio -p` (cwd = каталог сессии;
из env убирать `CLAUDECODE`, иначе сработает guard вложенной сессии).

- Вход: NDJSON `{"type":"user","message":{"role":"user","content":[{"type":"text","text":...}]}}`.
- Выход: `system/init` (session_id, model, tools, capabilities: `interrupt_receipt_v1`,
  `msg_lifecycle_v1`), `stream_event` (message_start / content_block_delta с
  `text_delta` и `input_json_delta` / message_stop), `assistant` (агрегированные
  блоки, включая `tool_use` с name+input), `result` (is_error, stop_reason,
  terminal_reason, result-текст, usage/cost).
- **Разрешения**: при `--permission-prompt-tool stdio` CLI шлёт
  `{"type":"control_request","request_id":...,"request":{"subtype":"can_use_tool",
  "tool_name","display_name","input","description","permission_suggestions","tool_use_id"}}`.
  Ответ (⚠️ `request_id` — ВНУТРИ `response`, не на верхнем уровне; с request_id
  на верхнем уровне CLI молча виснет):

  ```json
  {"type":"control_response","response":{"subtype":"success","request_id":"...",
    "response":{"behavior":"allow","updatedInput":{...}}}}
  ```

  Для отказа: `{"behavior":"deny","message":"причина"}`. После корректного allow
  инструмент выполняется и ход завершается `result`-ом. Проверено вживую.
- Отмена: control_request `interrupt` (client→CLI); capability
  `interrupt_receipt_v1` заявлена в init. Проверить вживую в Фазе 1.

## Зафиксированные сигнатуры coder/acp-go-sdk v0.13.5 (client-side)

- `acp.NewClientSideConnection(client Client, peerInput io.Writer, peerOutput io.Reader) *ClientSideConnection`;
  `.Done() <-chan struct{}`, `.SetLogger(*slog.Logger)`.
- Хелперы соединения: `Initialize(ctx, InitializeRequest) (InitializeResponse, error)`,
  `NewSession(ctx, NewSessionRequest{Cwd, McpServers}) (…{SessionId}, error)`,
  `Prompt(ctx, PromptRequest{SessionId, Prompt []ContentBlock}) (…{StopReason}, error)`,
  `Cancel(ctx, CancelNotification)`.
- `acp.Client` (10 методов): RequestPermission, SessionUpdate, ReadTextFile,
  WriteTextFile + 5 terminal-методов (можно возвращать ошибку, если capability
  `Terminal` не заявлена) — см. рабочую реализацию в `main.go` этого спайка.
- `SessionNotification.Update` — союз указателей: `AgentMessageChunk`,
  `AgentThoughtChunk`, `ToolCall{ToolCallId,Title,Status}`,
  `ToolCallUpdate{ToolCallId,Status *…}`, `Plan`, `AvailableCommandsUpdate`.
- Ответ на разрешение: `RequestPermissionResponse{Outcome: RequestPermissionOutcome{
  Selected: &…{OptionId}}}` либо `Cancelled: &…{}`; kinds:
  `PermissionOptionKindAllowOnce/AllowAlways/RejectOnce/…`.
- `acp.ProtocolVersionNumber` = 1 (совпало с ответом агента).

## Прогон боевого моста (Фаза 1)

`./acpspike -mode bridge` гоняет продакшн-код `internal/acp/claudebridge`
(in-process ACP agent-side поверх `io.Pipe`) против настоящего `claude`.
Проверено 2026-07-23: initialize → session/new → prompt, стриминг текста,
tool_call pending→in_progress→completed, `RequestPermission` с тремя опциями
(allow_once / allow_always / reject_once) — allow исполняет инструмент,
финальный `end_turn`, файл создан.

## Прочее

- `initialize` через lib-мост: protocol v1, `agentCapabilities.loadSession: true`,
  promptCapabilities: image+embeddedContext.
- Стоимость happy-path прогона (создание файла): ~$0.16, 2 хода.
- Запуск спайка: `go build ./cmd/acpspike && ./acpspike -mode lib|raw [-cwd DIR] [-prompt ...]`.
