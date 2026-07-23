package boardadapter

import (
	"context"
	"fmt"

	"github.com/mattermost/focalboard/server/app"
	"github.com/mattermost/focalboard/server/model"
	"github.com/mattermost/focalboard/server/utils"

	"github.com/mattermost/focalboard/mac-wails/internal/acp"
)

// Writer implements acp.BoardWriter over the server's app layer. All writes
// pass disableNotify=true: notify backends (incl. EventsBackend) are skipped
// so our own writes never re-trigger the agent, while the WebSocket broadcast
// still updates the UI live.
type Writer struct {
	app *app.App
}

var _ acp.BoardWriter = (*Writer)(nil)

func NewWriter(a *app.App) *Writer { return &Writer{app: a} }

// AddComment posts a comment block on the card.
func (w *Writer) AddComment(ctx context.Context, cardID, text string) error {
	card, err := w.app.GetCardByID(cardID)
	if err != nil {
		return fmt.Errorf("get card %s: %w", cardID, err)
	}
	now := utils.GetMillis()
	block := &model.Block{
		ID:        utils.NewID(utils.IDTypeBlock),
		BoardID:   card.BoardID,
		ParentID:  cardID,
		Type:      model.TypeComment,
		Title:     text,
		CreatedBy: model.SingleUser,
		CreateAt:  now,
		UpdateAt:  now,
	}
	_, err = w.app.InsertBlocksAndNotify([]*model.Block{block}, model.SingleUser, true)
	return err
}

// MoveCard sets the card's select property to optionID (post-MVP: used to
// advance the card after a successful session).
func (w *Writer) MoveCard(ctx context.Context, cardID, optionID string) error {
	card, err := w.app.GetCardByID(cardID)
	if err != nil {
		return fmt.Errorf("get card %s: %w", cardID, err)
	}
	board, err := w.app.GetBoard(card.BoardID)
	if err != nil {
		return fmt.Errorf("get board %s: %w", card.BoardID, err)
	}
	schema, err := model.ParsePropertySchema(board)
	if err != nil {
		return err
	}
	propID := ""
	for id, def := range schema {
		if def.Type == "select" {
			if _, ok := def.Options[optionID]; ok {
				propID = id
				break
			}
		}
	}
	if propID == "" {
		return fmt.Errorf("no select property on board %s has option %s", board.ID, optionID)
	}
	patch := &model.CardPatch{UpdatedProperties: map[string]any{propID: optionID}}
	_, err = w.app.PatchCard(patch, cardID, model.SingleUser, true)
	return err
}
