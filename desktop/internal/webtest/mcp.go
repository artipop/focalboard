package webtest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is reported to the client on initialize; it tracks the tool surface,
// not the app.
const version = "0.1.0"

// instructions arrive with the tool list, before any prompt we write elsewhere.
const instructions = `Инструменты ручного тестирования веб-приложения в настоящем браузере.
Адрес превью уже задан: открывать можно только его страницы, чужие хосты запрещены.

Как работать: open_page → snapshot → действия по ссылкам вида [e12] из снимка → snapshot снова.
Ссылки живут только до следующей перерисовки: если элемент «не найден», сделай новый snapshot.
Проверяй не только видимое — console_log и network_log показывают ошибки JS и упавшие запросы,
которые на глаз незаметны. Делай screenshot на ключевых шагах и обязательно на падении.
В конце вызови report_result — без него результат прогона никуда не попадёт.`

// Server holds what the tools work through: the browser, the run's output
// directory and the policy (allowed origin, known secrets).
type Server struct {
	cfg Config
	drv Driver
	art *Artifacts
}

type openInput struct {
	Path string `json:"path,omitempty" jsonschema:"путь или полный адрес в пределах превью; пусто — стартовая страница"`
}

type refInput struct {
	Ref string `json:"ref" jsonschema:"ссылка на элемент из последнего snapshot, например e12"`
}

type fillInput struct {
	Ref  string `json:"ref" jsonschema:"ссылка на поле из последнего snapshot"`
	Text string `json:"text" jsonschema:"что ввести; поле сначала очищается"`
}

type secretInput struct {
	Ref  string `json:"ref" jsonschema:"ссылка на поле из последнего snapshot"`
	Name string `json:"name" jsonschema:"имя секрета из списка доступных; само значение тебе не показывают"`
}

type selectInput struct {
	Ref   string `json:"ref" jsonschema:"ссылка на <select> из последнего snapshot"`
	Value string `json:"value" jsonschema:"видимый текст пункта"`
}

type keyInput struct {
	Key string `json:"key" jsonschema:"Enter, Escape, Tab, Backspace, Delete, Space, Home, End, PageUp, PageDown, стрелки или один символ"`
}

type waitInput struct {
	Text        string `json:"text,omitempty" jsonschema:"дождаться появления текста на странице"`
	Ref         string `json:"ref,omitempty" jsonschema:"дождаться появления элемента по ссылке из snapshot"`
	NetworkIdle bool   `json:"network_idle,omitempty" jsonschema:"дождаться, пока стихнут сетевые запросы"`
	Seconds     int    `json:"seconds,omitempty" jsonschema:"сколько ждать, по умолчанию 10"`
}

type screenshotInput struct {
	Name     string `json:"name" jsonschema:"короткое имя шага, попадёт в имя файла"`
	FullPage bool   `json:"full_page,omitempty" jsonschema:"снять страницу целиком, а не только видимую часть"`
}

type evalInput struct {
	Expression string `json:"expression" jsonschema:"JS-выражение, выполняется в текущей странице"`
}

type reportInput struct {
	Verdict string   `json:"verdict" jsonschema:"pass — сценарий прошёл; fail — найдены дефекты; blocked — проверить не удалось"`
	Summary string   `json:"summary" jsonschema:"итог в одну-две фразы"`
	Steps   []string `json:"steps,omitempty" jsonschema:"что именно было проделано, по шагам"`
	Bugs    []string `json:"bugs,omitempty" jsonschema:"найденные дефекты: что ожидалось и что произошло"`
}

// NewServer builds the MCP server exposing drv's operations as tools.
func NewServer(cfg Config, drv Driver, art *Artifacts) *mcp.Server {
	s := &Server{cfg: cfg, drv: drv, art: art}
	srv := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Title: "Web testing", Version: version},
		&mcp.ServerOptions{Instructions: s.instructions()},
	)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "open_page",
		Description: "Открыть страницу превью. Принимает путь (/checkout) или полный адрес того же сайта.",
	}, s.openPage)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "snapshot",
		Description: "Текстовый снимок страницы: заголовки, тексты и интерактивные элементы со ссылками [e12] для кликов и ввода.",
	}, s.snapshot)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "click",
		Description: "Кликнуть по элементу из последнего snapshot.",
	}, s.click)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fill",
		Description: "Ввести текст в поле из последнего snapshot (поле сначала очищается).",
	}, s.fill)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fill_secret",
		Description: "Ввести в поле заранее заданный секрет по имени (логин, пароль). Значение не показывается.",
	}, s.fillSecret)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "select_option",
		Description: "Выбрать пункт выпадающего списка по видимому тексту.",
	}, s.selectOption)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "hover",
		Description: "Навести курсор на элемент — для меню и подсказок, которые появляются при наведении.",
	}, s.hover)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "press_key",
		Description: "Нажать клавишу в текущем фокусе (Enter, Escape, Tab, стрелки).",
	}, s.pressKey)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "wait_for",
		Description: "Дождаться текста, элемента или затишья в сети. Без ожидания легко проверить страницу, которая ещё не дорисовалась.",
	}, s.waitFor)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "screenshot",
		Description: "Снимок экрана: он и есть доказательство результата, прикладывается к карточке.",
	}, s.screenshot)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "console_log",
		Description: "Сообщения консоли и необработанные исключения страницы с начала прогона.",
	}, s.consoleLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "network_log",
		Description: "Запросы, которые не удались: ответы со статусом 400+ и оборванные соединения.",
	}, s.networkLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "eval_js",
		Description: "Выполнить JS-выражение в странице — когда состояние иначе не проверить.",
	}, s.evalJS)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "report_result",
		Description: "Записать итог прогона. Обязательный последний шаг: от вердикта зависит, куда уедет карточка.",
	}, s.reportResult)

	return srv
}

// instructions append the run's own facts to the static text: where the preview
// lives and which secrets exist.
func (s *Server) instructions() string {
	var b strings.Builder
	b.WriteString(instructions)
	fmt.Fprintf(&b, "\n\nАдрес превью: %s", s.cfg.BaseURL)
	if names := s.cfg.SecretNames(); len(names) > 0 {
		fmt.Fprintf(&b, "\nДоступные секреты для fill_secret: %s", strings.Join(names, ", "))
	}
	return b.String()
}

// ServeStdio runs the MCP server on stdin/stdout until the client disconnects.
func ServeStdio(ctx context.Context, cfg Config, drv Driver, art *Artifacts) error {
	return NewServer(cfg, drv, art).Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) openPage(ctx context.Context, _ *mcp.CallToolRequest, in openInput) (*mcp.CallToolResult, any, error) {
	target, err := s.cfg.ResolveURL(in.Path)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	if err := s.drv.Navigate(ctx, target); err != nil {
		return errorResult("%v", err), nil, nil
	}
	snap, err := s.drv.Snapshot(ctx)
	if err != nil {
		return textResult("Открыто: %s\n(снимок страницы не удался: %v)", target, err), nil, nil
	}
	return textResult("Открыто: %s\n\n%s", target, snap), nil, nil
}

func (s *Server) snapshot(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	snap, err := s.drv.Snapshot(ctx)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("%s", snap), nil, nil
}

func (s *Server) click(ctx context.Context, _ *mcp.CallToolRequest, in refInput) (*mcp.CallToolResult, any, error) {
	if err := s.drv.Click(ctx, in.Ref); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("Клик по %s выполнен. Сделай snapshot, чтобы увидеть новое состояние.", in.Ref), nil, nil
}

func (s *Server) fill(ctx context.Context, _ *mcp.CallToolRequest, in fillInput) (*mcp.CallToolResult, any, error) {
	if err := s.drv.Fill(ctx, in.Ref, in.Text); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("В %s введено: %s", in.Ref, in.Text), nil, nil
}

func (s *Server) fillSecret(ctx context.Context, _ *mcp.CallToolRequest, in secretInput) (*mcp.CallToolResult, any, error) {
	value, ok := s.cfg.Secret(in.Name)
	if !ok {
		names := s.cfg.SecretNames()
		if len(names) == 0 {
			return errorResult("секреты для этого прогона не заданы"), nil, nil
		}
		return errorResult("секрет %q не задан; доступны: %s", in.Name, strings.Join(names, ", ")), nil, nil
	}
	if err := s.drv.Fill(ctx, in.Ref, value); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("В %s введён секрет %q.", in.Ref, in.Name), nil, nil
}

func (s *Server) selectOption(ctx context.Context, _ *mcp.CallToolRequest, in selectInput) (*mcp.CallToolResult, any, error) {
	if err := s.drv.SelectOption(ctx, in.Ref, in.Value); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("В %s выбрано %q.", in.Ref, in.Value), nil, nil
}

func (s *Server) hover(ctx context.Context, _ *mcp.CallToolRequest, in refInput) (*mcp.CallToolResult, any, error) {
	if err := s.drv.Hover(ctx, in.Ref); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("Курсор наведён на %s.", in.Ref), nil, nil
}

func (s *Server) pressKey(ctx context.Context, _ *mcp.CallToolRequest, in keyInput) (*mcp.CallToolResult, any, error) {
	if err := s.drv.Press(ctx, in.Key); err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("Нажата клавиша %s.", in.Key), nil, nil
}

// waitTimeout bounds an explicit wait; the model asks in whole seconds.
func waitTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 10 * time.Second
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) waitFor(ctx context.Context, _ *mcp.CallToolRequest, in waitInput) (*mcp.CallToolResult, any, error) {
	timeout := waitTimeout(in.Seconds)
	switch {
	case strings.TrimSpace(in.Text) != "":
		if err := s.drv.WaitText(ctx, in.Text, timeout); err != nil {
			return errorResult("%v", err), nil, nil
		}
		return textResult("Текст %q появился.", in.Text), nil, nil
	case strings.TrimSpace(in.Ref) != "":
		if err := s.drv.WaitRef(ctx, in.Ref, timeout); err != nil {
			return errorResult("%v", err), nil, nil
		}
		return textResult("Элемент %s появился.", in.Ref), nil, nil
	case in.NetworkIdle:
		if err := s.drv.WaitIdle(ctx, timeout); err != nil {
			return errorResult("%v", err), nil, nil
		}
		return textResult("Сеть успокоилась."), nil, nil
	default:
		return errorResult("укажи, чего ждать: text, ref или network_idle"), nil, nil
	}
}

func (s *Server) screenshot(ctx context.Context, _ *mcp.CallToolRequest, in screenshotInput) (*mcp.CallToolResult, any, error) {
	png, err := s.drv.Screenshot(ctx, in.FullPage)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	rel, err := s.art.SaveScreenshot(in.Name, png)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	text := "Скриншот сделан, но не сохранён (каталог артефактов не задан)."
	if rel != "" {
		text = fmt.Sprintf("Скриншот сохранён: %s", rel)
	}
	// The image goes back as an image so a model that can see gets to look, and
	// as a path so one that cannot still has something to put in the report.
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ImageContent{Data: png, MIMEType: "image/png"},
		&mcp.TextContent{Text: text},
	}}, nil, nil
}

func (s *Server) consoleLog(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	entries := s.drv.ConsoleLog()
	if len(entries) == 0 {
		return textResult("консоль пуста"), nil, nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.String()
	}
	return textResult("%s", strings.Join(lines, "\n")), nil, nil
}

func (s *Server) networkLog(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	entries := s.drv.NetworkLog()
	if len(entries) == 0 {
		return textResult("неудавшихся запросов нет"), nil, nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.String()
	}
	return textResult("%s", strings.Join(lines, "\n")), nil, nil
}

func (s *Server) evalJS(ctx context.Context, _ *mcp.CallToolRequest, in evalInput) (*mcp.CallToolResult, any, error) {
	value, err := s.drv.Eval(ctx, in.Expression)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	return textResult("%s", value), nil, nil
}

func (s *Server) reportResult(ctx context.Context, _ *mcp.CallToolRequest, in reportInput) (*mcp.CallToolResult, any, error) {
	verdict, err := NormalizeVerdict(in.Verdict)
	if err != nil {
		return errorResult("%v", err), nil, nil
	}
	if strings.TrimSpace(in.Summary) == "" {
		return errorResult("summary не может быть пустым: это то, что прочитает человек в карточке"), nil, nil
	}
	if verdict == VerdictFail && len(in.Bugs) == 0 {
		return errorResult("вердикт fail без списка bugs бесполезен — перечисли, что именно сломано"), nil, nil
	}
	current, err := s.drv.URL(ctx)
	if err != nil {
		current = s.cfg.BaseURL.String()
	}
	res := Result{
		Verdict: verdict,
		Summary: strings.TrimSpace(in.Summary),
		Steps:   in.Steps,
		Bugs:    in.Bugs,
		URL:     current,
		At:      time.Now(),
	}
	if err := s.art.WriteResult(res); err != nil {
		return errorResult("не удалось сохранить результат: %v", err), nil, nil
	}
	return textResult("Результат записан: %s. Прогон можно завершать.", verdict), nil, nil
}

func textResult(format string, args ...any) *mcp.CallToolResult {
	text := fmt.Sprintf(format, args...)
	if strings.TrimSpace(text) == "" {
		text = "(пустой ответ)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	res := textResult(format, args...)
	res.IsError = true
	return res
}
