package dokku

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerName is how the tools appear to an agent: mcp__dokku__deploy_branch etc.
const ServerName = "dokku"

// Environment the MCP process is configured through. Everything it may touch —
// which host, which repository, which branch — arrives here rather than as tool
// arguments, so the model cannot point a deploy somewhere else.
const (
	EnvTarget = "FOCALBOARD_DOKKU_TARGET" // JSON Target
	EnvRepo   = "FOCALBOARD_DOKKU_REPO"   // local repository the branch is pushed from
	EnvBranch = "FOCALBOARD_DOKKU_BRANCH" // default branch for tools that omit one
)

// version is reported to the client on initialize; it tracks the tool surface,
// not the app.
const version = "0.1.0"

// instructions tell the model what this server is for. They arrive with the
// tool list, before any prompt we write elsewhere.
const instructions = `Инструменты деплоя ветки на Dokku. Одна ветка — одно приложение,
доступное на адресе «репозиторий-ветка.базовый-домен». Хост, ключ, имя приложения
и домен уже заданы: их нельзя переопределить из инструментов, ты выбираешь только ветку.
Если деплой упал, посмотри app_logs — там вывод сборки и приложения.`

// deployInput is shared by every tool: an explicit branch, or the session's own.
type deployInput struct {
	Branch string `json:"branch,omitempty" jsonschema:"ветка репозитория; по умолчанию — ветка карточки"`
}

type logsInput struct {
	Branch string `json:"branch,omitempty" jsonschema:"ветка репозитория; по умолчанию — ветка карточки"`
	Lines  int    `json:"lines,omitempty" jsonschema:"сколько последних строк лога вернуть (по умолчанию 200)"`
}

type destroyInput struct {
	Branch  string `json:"branch,omitempty" jsonschema:"ветка, чьё превью удалить; по умолчанию — ветка карточки"`
	Confirm bool   `json:"confirm" jsonschema:"подтверждение удаления; без него ничего не произойдёт"`
}

// NewServer builds the MCP server exposing cl's operations as tools. artifacts
// is where the deploy outcome is recorded for the session that spawned us; an
// empty path records nothing.
func NewServer(cl *Client, artifacts string) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Title: "Dokku deploy", Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "deploy_branch",
		Description: "Задеплоить ветку на Dokku: создать приложение, если его ещё нет, назначить домен и запушить ветку. Возвращает URL и хвост лога сборки.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployInput) (*mcp.CallToolResult, any, error) {
		branch, err := cl.branch(ctx, in.Branch)
		if err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, cl.Target.Timeout())
		defer cancel()

		res, err := cl.Deploy(ctx, branch)
		recordOutcome(artifacts, res, branch, err)
		if err != nil {
			// The build log is the whole point of the failure report, so it goes
			// back to the model instead of a bare error string.
			return errorResult("Деплой не удался: %v\n\n%s", err, logBlock(res.PushLog)), nil, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Ветка `%s` задеплоена.\nURL: %s\nПриложение Dokku: %s", res.Branch, res.URL, res.App)
		if res.Created {
			b.WriteString("\n(приложение создано этим деплоем)")
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(&b, "\n%s", w)
		}
		fmt.Fprintf(&b, "\n\n%s", logBlock(res.PushLog))
		return textResult(b.String()), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "deployment_status",
		Description: "Состояние процессов приложения ветки на Dokku (dokku ps:report).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployInput) (*mcp.CallToolResult, any, error) {
		branch, err := cl.branch(ctx, in.Branch)
		if err != nil {
			return nil, nil, err
		}
		out, err := cl.Status(ctx, AppSlug(branch))
		if err != nil {
			return errorResult("не удалось получить статус: %v", err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "app_logs",
		Description: "Логи приложения ветки на Dokku: вывод сборки и запущенных процессов.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, any, error) {
		branch, err := cl.branch(ctx, in.Branch)
		if err != nil {
			return nil, nil, err
		}
		out, err := cl.Logs(ctx, AppSlug(branch), in.Lines)
		if err != nil {
			return errorResult("не удалось прочитать логи: %v", err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_deployments",
		Description: "Список превью-приложений этой цели на Dokku-хосте.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		apps, err := cl.List(ctx)
		if err != nil {
			return errorResult("не удалось получить список приложений: %v", err), nil, nil
		}
		if len(apps) == 0 {
			return textResult("превью-приложений нет"), nil, nil
		}
		return textResult(strings.Join(apps, "\n")), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "destroy_deployment",
		Description: "Удалить превью-приложение ветки с Dokku-хоста. Необратимо, требует confirm=true.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destroyInput) (*mcp.CallToolResult, any, error) {
		if !in.Confirm {
			return errorResult("удаление не подтверждено: вызови ещё раз с confirm=true"), nil, nil
		}
		branch, err := cl.branch(ctx, in.Branch)
		if err != nil {
			return nil, nil, err
		}
		out, err := cl.Destroy(ctx, AppSlug(branch))
		if err != nil {
			return errorResult("не удалось удалить приложение: %v", err), nil, nil
		}
		return textResult(fmt.Sprintf("🗑 Приложение %s удалено.\n%s", cl.Target.AppName(AppSlug(branch)), strings.TrimSpace(out))), nil, nil
	})

	return srv
}

// ServeStdio runs the MCP server on stdin/stdout until the client disconnects.
func ServeStdio(ctx context.Context, cl *Client, artifacts string) error {
	return NewServer(cl, artifacts).Run(ctx, &mcp.StdioTransport{})
}

// recordOutcome files what the attempt did. A failure to record is not worth
// failing the tool call over — the model already has the real answer — but it
// must not pass silently either, so it goes to stderr, which is the server's log.
func recordOutcome(dir string, res Result, branch string, deployErr error) {
	if dir == "" {
		return
	}
	o := Outcome{OK: deployErr == nil, App: res.App, Branch: res.Branch, URL: res.URL}
	if o.Branch == "" {
		o.Branch = branch
	}
	if deployErr != nil {
		o.Error = deployErr.Error()
	}
	if err := WriteOutcome(dir, o); err != nil {
		fmt.Fprintf(os.Stderr, "mcp dokku: не удалось записать %s: %v\n", OutcomeFile, err)
	}
}

// branch resolves the branch a tool call works on: the explicit argument, the
// branch the session was started for, or whatever the repository has checked out.
func (c *Client) branch(ctx context.Context, explicit string) (string, error) {
	if b := strings.TrimSpace(explicit); b != "" {
		return b, nil
	}
	if c.Branch != "" {
		return c.Branch, nil
	}
	if c.Repo == "" {
		return "", fmt.Errorf("ветка не указана и определить её неоткуда — передай branch")
	}
	return CurrentBranch(ctx, c.Run, c.Repo)
}

func textResult(text string) *mcp.CallToolResult {
	if strings.TrimSpace(text) == "" {
		text = "(пустой ответ)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	res := textResult(fmt.Sprintf(format, args...))
	res.IsError = true
	return res
}

func logBlock(log string) string {
	log = strings.TrimSpace(log)
	if log == "" {
		return "(вывод git push пуст)"
	}
	return "--- git push ---\n" + log
}
