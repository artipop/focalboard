package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// An agent that needs a decision from the person asks for it as a *form*: ACP's
// elicitation, which the claude adapter uses to surface its own AskUserQuestion
// tool and any MCP server's elicitation. The tool is only offered to a client
// that says it can render one — the adapter's own rule is
//
//	const disallowedTools = elicitationSupport.form ? [] : ["AskUserQuestion"]
//
// so an agent asks nothing of a client that stays silent about it, and states
// its question in the answer instead. We say we can (see Initialize), and this
// file is what makes that true.
//
// Nothing here knows about AskUserQuestion: what arrives is a JSON Schema, what
// goes back is an object keyed by the schema's own property names. That is also
// what makes it work for an MCP server's elicitation, which is the same call
// with a schema somebody else wrote.

// clientCapabilities is what we tell every agent we can do. Saying we can draw
// a form is not a formality: it is the difference between an agent that can ask
// the user something mid-task and one that can only say what it would have
// asked. Terminals are deliberately absent — we do not run the agent's commands
// for it.
func clientCapabilities() acpsdk.ClientCapabilities {
	return acpsdk.ClientCapabilities{
		Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		Elicitation: &acpsdk.ElicitationCapabilities{
			Form: &acpsdk.ElicitationFormCapabilities{},
		},
	}
}

// ElicitationForm is a form to put in front of the user, flattened out of the
// schema the agent sent so the console has nothing left to interpret.
type ElicitationForm struct {
	Message string             `json:"message"`
	Fields  []ElicitationField `json:"fields"`
}

// ElicitationField is one input. Type is what the console draws:
// select/multiSelect with options, or a plain text/number/boolean field.
type ElicitationField struct {
	Key         string              `json:"key"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Type        string              `json:"type"`
	Options     []ElicitationOption `json:"options,omitempty"`
	Required    bool                `json:"required,omitempty"`

	// CustomFor names the select this free-text field answers instead of. The
	// adapter pairs the two through `_meta`, so "Other" can be drawn under its
	// own question rather than as a field of its own.
	CustomFor string `json:"customFor,omitempty"`
}

// ElicitationOption is one choice of a select field.
type ElicitationOption struct {
	Value       string `json:"value"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// Field types, as the console draws them.
const (
	fieldSelect      = "select"
	fieldMultiSelect = "multiSelect"
	fieldText        = "text"
	fieldNumber      = "number"
	fieldBoolean     = "boolean"
)

// customAnswerMetaKey is how the claude adapter marks the free-text field that
// belongs to a question, and names the question it belongs to.
const customAnswerMetaKey = "_askUserQuestionCustomAnswer"

// pendingElicitation is one form waiting for a human.
type pendingElicitation struct {
	sessionID string
	answer    chan map[string]any
}

// UnstableCreateElicitation shows the agent's form and returns what the user
// filled in. Declared on sessionClient alone — the SDK looks the method up by
// interface assertion, which is what "unstable" means here.
//
// A session with nobody watching declines at once rather than holding the turn
// until the prompt times out: there is no one to fill anything in, exactly as
// with a permission request.
func (c *sessionClient) UnstableCreateElicitation(ctx context.Context, params acpsdk.UnstableCreateElicitationRequest) (acpsdk.UnstableCreateElicitationResponse, error) {
	if params.Form == nil {
		// We advertise form elicitation only, so anything else is a mode we
		// cannot draw — say so instead of leaving the agent waiting.
		c.m.log.Info("acp: declined a non-form elicitation", "session", c.s.ID)
		return declineElicitation(), nil
	}
	form := elicitationForm(*params.Form)
	if len(form.Fields) == 0 {
		c.m.log.Info("acp: declined an elicitation with no fields", "session", c.s.ID)
		return declineElicitation(), nil
	}
	if !c.s.hasConsole() {
		c.m.tr.Event(c.s.ID, TraceApp, "elicitation_declined", map[string]any{"reason": "no console attached"})
		c.m.log.Info("acp: elicitation declined — no console attached",
			"session", c.s.ID, "message", form.Message)
		c.s.appendEvent(c.m, "elicitation", map[string]any{
			"message":  form.Message,
			"fields":   form.Fields,
			"declined": "нет открытой консоли — некому отвечать",
		})
		return declineElicitation(), nil
	}
	return c.askForm(ctx, form)
}

// askForm puts the form in front of the console and waits for it.
func (c *sessionClient) askForm(ctx context.Context, form ElicitationForm) (acpsdk.UnstableCreateElicitationResponse, error) {
	c.flush() // the form must land after the text that explains it

	requestID := uuid.NewString()
	answer := c.m.registerElicitation(requestID, c.s.ID)
	defer c.m.forgetElicitation(requestID)

	payload := map[string]any{
		"requestId": requestID,
		"message":   form.Message,
		"fields":    form.Fields,
		"pending":   true,
	}
	c.s.appendEvent(c.m, "elicitation", payload)
	c.m.setStatus(c.s, StatusWaitingPermission)
	defer c.m.setStatus(c.s, StatusRunning)

	event := map[string]any{"sessionId": c.s.ID, "cardId": c.s.CardID}
	for k, v := range payload {
		event[k] = v
	}
	c.m.ui.Emit(EventElicitation, event)

	timeout := time.NewTimer(c.m.cfg.PermissionTimeout())
	defer timeout.Stop()

	select {
	case content := <-answer:
		c.s.appendEvent(c.m, "elicitation", map[string]any{
			"requestId": requestID,
			"message":   form.Message,
			"answered":  answerSummary(form, content),
		})
		return acpsdk.UnstableCreateElicitationResponse{
			Accept: &acpsdk.UnstableCreateElicitationAccept{Action: "accept", Content: content},
		}, nil

	case <-timeout.C:
		c.m.tr.Event(c.s.ID, TraceApp, "elicitation_timeout", map[string]any{"requestId": requestID})
		c.m.log.Info("acp: elicitation timed out", "session", c.s.ID, "message", form.Message)
		c.s.appendEvent(c.m, "elicitation", map[string]any{
			"requestId": requestID,
			"message":   form.Message,
			"declined":  "никто не ответил",
		})
		return declineElicitation(), nil

	case <-ctx.Done():
		// The turn is going away; cancelling is what says "not answered" rather
		// than "answered with nothing".
		return acpsdk.UnstableCreateElicitationResponse{
			Cancel: &acpsdk.UnstableCreateElicitationCancel{Action: "cancel"},
		}, nil
	}
}

func declineElicitation() acpsdk.UnstableCreateElicitationResponse {
	return acpsdk.UnstableCreateElicitationResponse{
		Decline: &acpsdk.UnstableCreateElicitationDecline{Action: "decline"},
	}
}

// UnstableCompleteElicitation tells the client a url-mode elicitation finished
// elsewhere. We never open one, so there is nothing to dismiss.
func (c *sessionClient) UnstableCompleteElicitation(ctx context.Context, params acpsdk.UnstableCompleteElicitationNotification) error {
	return nil
}

// elicitationForm flattens the requested schema into fields. Order matters to
// the person answering and JSON objects have none, so fields come out sorted by
// key — which is the order the adapter numbers its questions in (`q0`, `q1`, …).
func elicitationForm(form acpsdk.UnstableCreateElicitationForm) ElicitationForm {
	required := map[string]bool{}
	for _, name := range form.RequestedSchema.Required {
		required[name] = true
	}
	keys := make([]string, 0, len(form.RequestedSchema.Properties))
	for key := range form.RequestedSchema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]ElicitationField, 0, len(keys))
	for _, key := range keys {
		spec, ok := form.RequestedSchema.Properties[key].(map[string]any)
		if !ok {
			continue
		}
		field := elicitationField(key, spec)
		field.Required = required[key]
		fields = append(fields, field)
	}
	return ElicitationForm{Message: strings.TrimSpace(form.Message), Fields: fields}
}

// elicitationField reads one property of the schema. A field is a select when
// it lists its values (`oneOf`, `enum`, or `items.anyOf` for several answers);
// anything else is the plain input its JSON type asks for.
func elicitationField(key string, spec map[string]any) ElicitationField {
	field := ElicitationField{
		Key:         key,
		Title:       stringAt(spec, "title"),
		Description: stringAt(spec, "description"),
		Type:        fieldText,
	}
	if meta, ok := spec["_meta"].(map[string]any); ok {
		if custom, ok := meta[customAnswerMetaKey].(map[string]any); ok {
			field.CustomFor = stringAt(custom, "questionId")
		}
	}

	switch stringAt(spec, "type") {
	case "array":
		field.Type = fieldMultiSelect
		if items, ok := spec["items"].(map[string]any); ok {
			field.Options = elicitationOptions(items)
		}
	case "number", "integer":
		field.Type = fieldNumber
	case "boolean":
		field.Type = fieldBoolean
	}

	if options := elicitationOptions(spec); len(options) > 0 {
		field.Options = options
		if field.Type != fieldMultiSelect {
			field.Type = fieldSelect
		}
	}
	if field.Type == fieldMultiSelect && len(field.Options) == 0 {
		// A list of free values is not something the console can draw as a
		// picker; one line of text is closer to the truth than an empty list.
		field.Type = fieldText
	}
	return field
}

// elicitationOptions reads the values a field offers, in the three spellings a
// JSON Schema uses for them.
func elicitationOptions(spec map[string]any) []ElicitationOption {
	var out []ElicitationOption
	for _, key := range []string{"oneOf", "anyOf"} {
		list, ok := spec[key].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			option, ok := item.(map[string]any)
			if !ok {
				continue
			}
			value := option["const"]
			if value == nil {
				continue
			}
			out = append(out, ElicitationOption{
				Value:       fmt.Sprint(value),
				Title:       stringAt(option, "title"),
				Description: stringAt(option, "description"),
			})
		}
	}
	if len(out) > 0 {
		return out
	}
	if list, ok := spec["enum"].([]any); ok {
		for _, value := range list {
			out = append(out, ElicitationOption{Value: fmt.Sprint(value)})
		}
	}
	return out
}

func stringAt(spec map[string]any, key string) string {
	if s, ok := spec[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// answerSummary is what the card and a second console see: the answer in
// words, not the raw object. A field's title is used where it has one, since
// the keys are the agent's own bookkeeping (`q0`).
func answerSummary(form ElicitationForm, content map[string]any) string {
	titles := make(map[string]string, len(form.Fields))
	for _, field := range form.Fields {
		titles[field.Key] = firstNonEmpty(field.Title, field.Description, field.Key)
	}
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		text := strings.TrimSpace(fmt.Sprint(content[key]))
		if text == "" || text == "[]" || text == "<nil>" {
			continue
		}
		if title := titles[key]; title != "" && title != key {
			parts = append(parts, title+": "+text)
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "; ")
}

func (m *Manager) registerElicitation(requestID, sessionID string) chan map[string]any {
	ch := make(chan map[string]any, 1)
	m.elicitMu.Lock()
	if m.elicits == nil {
		m.elicits = map[string]pendingElicitation{}
	}
	m.elicits[requestID] = pendingElicitation{sessionID: sessionID, answer: ch}
	m.elicitMu.Unlock()
	return ch
}

func (m *Manager) forgetElicitation(requestID string) {
	m.elicitMu.Lock()
	delete(m.elicits, requestID)
	m.elicitMu.Unlock()
}

// AnswerElicitation delivers what the user filled in. contentJSON is an object
// keyed by the field keys the form carried — the console echoes back the
// schema's own names, so nothing has to be translated on the way in or out.
func (m *Manager) AnswerElicitation(sessionID, requestID, contentJSON string) error {
	m.tr.Event(sessionID, TraceFromUI, "AnswerElicitation", map[string]any{"requestId": requestID})

	content := map[string]any{}
	if strings.TrimSpace(contentJSON) != "" {
		if err := json.Unmarshal([]byte(contentJSON), &content); err != nil {
			return fmt.Errorf("ответ на форму должен быть JSON-объектом: %w", err)
		}
	}

	m.elicitMu.Lock()
	p, ok := m.elicits[requestID]
	m.elicitMu.Unlock()
	if !ok || p.sessionID != sessionID {
		m.tr.Event(sessionID, TraceApp, "answer_unmatched", map[string]any{"requestId": requestID})
		return fmt.Errorf("форма %s больше не ждёт ответа", requestID)
	}
	select {
	case p.answer <- content:
		return nil
	default:
		return fmt.Errorf("на форму %s уже ответили", requestID)
	}
}
