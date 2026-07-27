// Package boardadapter bridges the Focalboard server to the agnostic
// internal/acp package. It is the only package allowed to import both sides:
// replacing the board backend later means replacing only this package.
package boardadapter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/focalboard/server/app"
	"github.com/mattermost/focalboard/server/model"
	"github.com/mattermost/focalboard/server/services/notify"
	"github.com/mattermost/mattermost/server/public/shared/mlog"

	"github.com/mattermost/focalboard/desktop/internal/acp"
)

// EventsBackend is a notify.Backend that normalizes card select-property
// changes into acp.CardMoved events.
type EventsBackend struct {
	log mlog.LoggerIFace
	ch  chan acp.CardMoved

	mu  sync.Mutex
	app *app.App // set after server construction; used to fetch card bodies
}

var (
	_ notify.Backend  = (*EventsBackend)(nil)
	_ acp.BoardEvents = (*EventsBackend)(nil)
)

// NewEventsBackend creates the backend. Pass it into server.Params.NotifyBackends.
func NewEventsBackend(log mlog.LoggerIFace) *EventsBackend {
	return &EventsBackend{log: log, ch: make(chan acp.CardMoved, 256)}
}

// SetApp late-binds the app layer (available only after server.New returns).
func (b *EventsBackend) SetApp(a *app.App) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.app = a
}

func (b *EventsBackend) Name() string    { return "acpTrigger" }
func (b *EventsBackend) Start() error    { return nil }
func (b *EventsBackend) ShutDown() error { return nil }

// Subscribe returns the normalized event stream.
func (b *EventsBackend) Subscribe(ctx context.Context) (<-chan acp.CardMoved, error) {
	return b.ch, nil
}

// BlockChanged runs on the notify worker goroutine; it must stay fast and
// never block (buffered channel, drop on overflow).
func (b *EventsBackend) BlockChanged(evt notify.BlockChangeEvent) error {
	if evt.Action != notify.Update || evt.Board == nil ||
		evt.BlockChanged == nil || evt.BlockChanged.Type != model.TypeCard || evt.BlockOld == nil {
		return nil
	}

	schema, err := model.ParsePropertySchema(evt.Board)
	if err != nil {
		return fmt.Errorf("parse board schema: %w", err)
	}
	oldProps := rawProperties(evt.BlockOld)
	newProps := rawProperties(evt.BlockChanged)

	for propID, def := range schema {
		if def.Type != "select" {
			continue
		}
		oldVal := stringValue(oldProps[propID])
		newVal := stringValue(newProps[propID])
		if oldVal == newVal {
			continue
		}
		ev := acp.CardMoved{
			EventID:     uuid.NewString(),
			CardID:      evt.BlockChanged.ID,
			BoardID:     evt.Board.ID,
			Title:       evt.BlockChanged.Title,
			Body:        b.cardBody(evt.Board.ID, evt.BlockChanged),
			Props:       namedProperties(evt.BlockChanged, schema),
			OptionNames: selectedOptionNames(newProps, schema),
			FromColumn:  column(def, oldVal),
			ToColumn:    column(def, newVal),
			At:          time.Now(),
		}
		select {
		case b.ch <- ev:
		default:
			b.log.Warn("acpTrigger: event channel full, dropping card move", mlog.String("card", ev.CardID))
		}
	}
	return nil
}

// CardByID reads a card on demand so a console can be opened on it without
// moving it into the trigger column. The returned event carries no columns —
// nothing moved — but everything repo/agent resolution needs.
func (b *EventsBackend) CardByID(ctx context.Context, cardID string) (acp.CardMoved, error) {
	b.mu.Lock()
	a := b.app
	b.mu.Unlock()
	if a == nil {
		return acp.CardMoved{}, fmt.Errorf("board app is not ready")
	}
	block, err := a.GetBlockByID(cardID)
	if err != nil {
		return acp.CardMoved{}, fmt.Errorf("get card %s: %w", cardID, err)
	}
	if block == nil || block.Type != model.TypeCard {
		return acp.CardMoved{}, fmt.Errorf("block %s is not a card", cardID)
	}
	board, err := a.GetBoard(block.BoardID)
	if err != nil {
		return acp.CardMoved{}, fmt.Errorf("get board %s: %w", block.BoardID, err)
	}
	schema, err := model.ParsePropertySchema(board)
	if err != nil {
		return acp.CardMoved{}, fmt.Errorf("parse board schema: %w", err)
	}
	return acp.CardMoved{
		EventID:     uuid.NewString(),
		CardID:      block.ID,
		BoardID:     board.ID,
		Title:       block.Title,
		Body:        b.cardBody(board.ID, block),
		Props:       namedProperties(block, schema),
		OptionNames: selectedOptionNames(rawProperties(block), schema),
		At:          time.Now(),
	}, nil
}

func column(def model.PropDef, optionID string) acp.Column {
	name := ""
	if opt, ok := def.Options[optionID]; ok {
		name = opt.Value
	}
	return acp.Column{
		PropertyID:   def.ID,
		PropertyName: def.Name,
		OptionID:     optionID,
		Name:         name,
	}
}

func rawProperties(block *model.Block) map[string]any {
	if block == nil {
		return nil
	}
	props, _ := block.Fields["properties"].(map[string]any)
	return props
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// selectedOptionNames collects the display names of every selected
// select/multiSelect option on the card — the "tags" used to map the card to
// a registered repository.
func selectedOptionNames(props map[string]any, schema model.PropSchema) []string {
	var out []string
	appendOption := func(def model.PropDef, v any) {
		id, ok := v.(string)
		if !ok {
			return
		}
		if opt, ok := def.Options[id]; ok && opt.Value != "" {
			out = append(out, opt.Value)
		}
	}
	for propID, def := range schema {
		v, ok := props[propID]
		if !ok || v == nil {
			continue
		}
		switch def.Type {
		case "select":
			appendOption(def, v)
		case "multiSelect":
			if vals, ok := v.([]any); ok {
				for _, item := range vals {
					appendOption(def, item)
				}
			}
		}
	}
	return out
}

// namedProperties resolves the card's properties into a lowercased
// name → display-value map (repo_path, branch, …).
func namedProperties(block *model.Block, schema model.PropSchema) map[string]string {
	out := make(map[string]string)
	parsed, err := model.ParseProperties(block, schema, nil)
	if err != nil {
		return out
	}
	for _, prop := range parsed {
		if prop.Name == "" {
			continue
		}
		out[strings.ToLower(prop.Name)] = prop.Value
	}
	return out
}

// cardBody joins the card's text content blocks in contentOrder order.
func (b *EventsBackend) cardBody(boardID string, card *model.Block) string {
	b.mu.Lock()
	a := b.app
	b.mu.Unlock()
	if a == nil {
		return ""
	}
	blocks, err := a.GetBlocks(boardID, card.ID, string(model.TypeText))
	if err != nil || len(blocks) == 0 {
		return ""
	}

	order := map[string]int{}
	if rawOrder, ok := card.Fields["contentOrder"].([]any); ok {
		for i, v := range rawOrder {
			if id, ok := v.(string); ok {
				order[id] = i
			}
		}
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		oi, iok := order[blocks[i].ID]
		oj, jok := order[blocks[j].ID]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		default:
			return false
		}
	})

	var parts []string
	for _, blk := range blocks {
		if t := strings.TrimSpace(blk.Title); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}
