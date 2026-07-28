package boardadapter

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/focalboard/server/app"
	"github.com/mattermost/focalboard/server/model"
	"github.com/mattermost/focalboard/server/utils"

	"github.com/mattermost/focalboard/desktop/internal/acp"
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

// MoveCardByOptionName moves a card to a column named in the config rather than
// identified by id — "Tested", not "a7f3…". Property and option are matched
// case-insensitively, as the trigger columns are.
func (w *Writer) MoveCardByOptionName(ctx context.Context, cardID, propertyName, optionName string) error {
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
	propID, optionID, ok := findSelectOption(schema, propertyName, optionName)
	if !ok {
		return fmt.Errorf("на доске %s нет колонки %q в свойстве %q", board.ID, optionName, propertyName)
	}
	patch := &model.CardPatch{UpdatedProperties: map[string]any{propID: optionID}}
	_, err = w.app.PatchCard(patch, cardID, model.SingleUser, true)
	return err
}

// findSelectOption resolves a (property name, option name) pair to the ids the
// card actually stores. Matching is case-insensitive, like the trigger columns.
func findSelectOption(schema model.PropSchema, propertyName, optionName string) (propID, optionID string, ok bool) {
	for id, def := range schema {
		if def.Type != "select" || !strings.EqualFold(def.Name, propertyName) {
			continue
		}
		for oid, opt := range def.Options {
			if strings.EqualFold(opt.Value, optionName) {
				return id, oid, true
			}
		}
	}
	return "", "", false
}

// AttachFile puts a file into the card's content: an image is rendered inline,
// anything else becomes a download. This is how a test run's screenshots end up
// where a human reads the result, instead of in a directory nobody opens.
func (w *Writer) AttachFile(ctx context.Context, cardID, filename, mime string, data []byte) error {
	card, err := w.app.GetCardByID(cardID)
	if err != nil {
		return fmt.Errorf("get card %s: %w", cardID, err)
	}
	board, err := w.app.GetBoard(card.BoardID)
	if err != nil {
		return fmt.Errorf("get board %s: %w", card.BoardID, err)
	}
	fileID, err := w.app.SaveFile(bytes.NewReader(data), board.TeamID, card.BoardID, filename, false)
	if err != nil {
		return fmt.Errorf("save file %s: %w", filename, err)
	}

	blockType := model.BlockType(model.TypeAttachment)
	if strings.HasPrefix(mime, "image/") {
		blockType = model.TypeImage
	}
	now := utils.GetMillis()
	block := &model.Block{
		ID:        utils.NewID(model.BlockType2IDType(blockType)),
		BoardID:   card.BoardID,
		ParentID:  cardID,
		Type:      blockType,
		Title:     filename,
		Fields:    map[string]any{"fileId": fileID},
		CreatedBy: model.SingleUser,
		CreateAt:  now,
		UpdateAt:  now,
	}
	if _, err := w.app.InsertBlocksAndNotify([]*model.Block{block}, model.SingleUser, true); err != nil {
		return fmt.Errorf("insert %s block: %w", blockType, err)
	}
	return w.appendContent(cardID, block.ID)
}

// appendContent adds a block to the end of the card's content. The card's raw
// contentOrder is patched rather than model.CardPatch's flat []string, because
// the field may hold nested arrays (side-by-side rows) that flattening would
// silently rearrange.
func (w *Writer) appendContent(cardID, blockID string) error {
	block, err := w.app.GetBlockByID(cardID)
	if err != nil {
		return fmt.Errorf("get card block %s: %w", cardID, err)
	}
	patch := &model.BlockPatch{UpdatedFields: map[string]any{
		"contentOrder": appendContentOrder(block.Fields, blockID),
	}}
	_, err = w.app.PatchBlockAndNotify(cardID, patch, model.SingleUser, true)
	return err
}

// appendContentOrder returns the card's content order with blockID at the end,
// preserving whatever nesting it already had.
func appendContentOrder(fields map[string]any, blockID string) []any {
	order := []any{}
	if existing, ok := fields["contentOrder"].([]any); ok {
		order = append(order, existing...)
	}
	return append(order, blockID)
}
