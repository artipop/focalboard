package boardadapter

import (
	"context"
	"testing"

	"github.com/mattermost/focalboard/server/model"
	"github.com/mattermost/focalboard/server/services/notify"
	"github.com/mattermost/mattermost/server/public/shared/mlog"

	"github.com/mattermost/focalboard/desktop/internal/acp"
)

func testBoard() *model.Board {
	return &model.Board{
		ID:     "board1",
		TeamID: "team1",
		CardProperties: []map[string]any{
			{
				"id":   "prop-status",
				"name": "Status",
				"type": "select",
				"options": []any{
					map[string]any{"id": "opt-backlog", "value": "Backlog", "color": ""},
					map[string]any{"id": "opt-agent", "value": "To Agent", "color": ""},
				},
			},
			{
				"id":   "prop-repo",
				"name": "repo_path",
				"type": "text",
			},
		},
	}
}

func cardBlock(status string) *model.Block {
	return &model.Block{
		ID:      "card1",
		BoardID: "board1",
		Type:    model.TypeCard,
		Title:   "My task",
		Fields: map[string]any{
			"properties": map[string]any{
				"prop-status": status,
				"prop-repo":   "/tmp/repo",
			},
		},
	}
}

func drain(t *testing.T, b *EventsBackend) []acp.CardMoved {
	t.Helper()
	ch, err := b.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []acp.CardMoved
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestBlockChangedEmitsExactlyOneMove(t *testing.T) {
	b := NewEventsBackend(mlog.CreateConsoleTestLogger(t))
	err := b.BlockChanged(notify.BlockChangeEvent{
		Action:       notify.Update,
		Board:        testBoard(),
		BlockChanged: cardBlock("opt-agent"),
		BlockOld:     cardBlock("opt-backlog"),
	})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, b)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.FromColumn.Name != "Backlog" || ev.ToColumn.Name != "To Agent" {
		t.Errorf("wrong columns: from=%q to=%q", ev.FromColumn.Name, ev.ToColumn.Name)
	}
	if ev.ToColumn.PropertyName != "Status" {
		t.Errorf("wrong property name %q", ev.ToColumn.PropertyName)
	}
	if ev.Props["repo_path"] != "/tmp/repo" {
		t.Errorf("props not resolved: %v", ev.Props)
	}
	if ev.Title != "My task" || ev.CardID != "card1" || ev.BoardID != "board1" {
		t.Errorf("bad identity fields: %+v", ev)
	}
}

func TestBlockChangedIgnoresIrrelevantChanges(t *testing.T) {
	b := NewEventsBackend(mlog.CreateConsoleTestLogger(t))
	board := testBoard()

	// Same select value → no event.
	if err := b.BlockChanged(notify.BlockChangeEvent{
		Action: notify.Update, Board: board,
		BlockChanged: cardBlock("opt-agent"), BlockOld: cardBlock("opt-agent"),
	}); err != nil {
		t.Fatal(err)
	}
	// Add action → no event.
	if err := b.BlockChanged(notify.BlockChangeEvent{
		Action: notify.Add, Board: board,
		BlockChanged: cardBlock("opt-agent"),
	}); err != nil {
		t.Fatal(err)
	}
	// Comment block → no event.
	comment := &model.Block{ID: "cmt1", BoardID: "board1", Type: model.TypeComment, Title: "hi"}
	if err := b.BlockChanged(notify.BlockChangeEvent{
		Action: notify.Update, Board: board,
		BlockChanged: comment, BlockOld: comment,
	}); err != nil {
		t.Fatal(err)
	}
	// Text-property change on a card → no event.
	changed := cardBlock("opt-agent")
	changed.Fields["properties"].(map[string]any)["prop-repo"] = "/tmp/other"
	if err := b.BlockChanged(notify.BlockChangeEvent{
		Action: notify.Update, Board: board,
		BlockChanged: changed, BlockOld: cardBlock("opt-agent"),
	}); err != nil {
		t.Fatal(err)
	}

	if events := drain(t, b); len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
}
