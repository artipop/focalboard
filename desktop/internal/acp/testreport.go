package acp

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/webtest"
)

// What a finished test session leaves on the card: the verdict as a comment,
// the screenshots as attachments, and — when the verdict is clear and a column
// is configured — the card itself in Tested or Failed.

// maxAttachments bounds how many screenshots one run may put on a card. A long
// scenario can take dozens; the card is for reading, and the rest stay in the
// artifacts directory.
const maxAttachments = 12

// reportTestRun is the whole outcome of a test session. turnErr is whatever the
// turn itself failed with (timeout, agent crash) — evidence collected before
// that is still worth reporting.
func (m *Manager) reportTestRun(s *Session, finalText string, turnErr error) {
	res, err := m.readTestResult(s)
	m.comment(s, testComment(s, res, err, finalText, turnErr))
	m.attachTestArtifacts(s, res)
	if err == nil {
		m.moveAfterTest(s, res)
	}
}

// readTestResult loads what the agent reported. A missing file is not an error
// in the code — it is the agent having skipped report_result — so it is told
// apart from a broken one.
func (m *Manager) readTestResult(s *Session) (webtest.Result, error) {
	if s.Test == nil || s.Test.Artifacts == "" {
		return webtest.Result{}, fmt.Errorf("каталог артефактов не задан")
	}
	res, err := webtest.ReadResult(s.Test.Artifacts)
	if os.IsNotExist(err) {
		return webtest.Result{}, fmt.Errorf("агент не вызвал report_result — вердикта нет")
	}
	if err != nil {
		return webtest.Result{}, err
	}
	return res, nil
}

// testComment is what a person reads on the card.
func testComment(s *Session, res webtest.Result, resErr error, finalText string, turnErr error) string {
	var b strings.Builder
	switch {
	case resErr != nil:
		b.WriteString("Тестирование завершено, но вердикта нет.\n\n")
		fmt.Fprintf(&b, "%s\n\n", resErr)
	case res.Passed():
		b.WriteString("Тест пройден.\n\n")
	case res.Verdict == webtest.VerdictBlocked:
		b.WriteString("🚧 Протестировать не удалось.\n\n")
	default:
		b.WriteString("Тест не пройден.\n\n")
	}
	if turnErr != nil {
		fmt.Fprintf(&b, "Сессия прервалась: %s\n\n", turnErr)
	}
	if res.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", res.Summary)
	}
	if len(res.Bugs) > 0 {
		b.WriteString("**Дефекты**\n")
		for _, bug := range res.Bugs {
			fmt.Fprintf(&b, "- %s\n", bug)
		}
		b.WriteString("\n")
	}
	if len(res.Steps) > 0 {
		b.WriteString("**Что проверено**\n")
		for _, step := range res.Steps {
			fmt.Fprintf(&b, "- %s\n", step)
		}
		b.WriteString("\n")
	}
	// The agent's own last message only matters when there is no structured
	// report to read instead.
	if resErr != nil {
		if t := strings.TrimSpace(finalText); t != "" {
			fmt.Fprintf(&b, "%s\n\n", truncateRunes(t, 4000))
		}
	}
	if s.Test != nil {
		fmt.Fprintf(&b, "Превью: %s\n", s.Test.URL)
		if s.Test.Artifacts != "" {
			fmt.Fprintf(&b, "Артефакты: `%s`", s.Test.Artifacts)
		}
	}
	return strings.TrimSpace(b.String())
}

// attachTestArtifacts puts the screenshots on the card. Failures are logged and
// not surfaced: the verdict is already there, and a card that cannot take an
// attachment is not a test result.
func (m *Manager) attachTestArtifacts(s *Session, res webtest.Result) {
	if s.Test == nil || s.Test.Artifacts == "" {
		return
	}
	shots := res.Screenshots
	if len(shots) == 0 {
		found, err := webtest.ListScreenshots(s.Test.Artifacts)
		if err != nil {
			m.log.Warn("acp: cannot list test screenshots", "session", s.ID, "err", err)
			return
		}
		shots = found
	}
	if len(shots) > maxAttachments {
		shots = shots[:maxAttachments]
	}
	for _, rel := range shots {
		data, err := os.ReadFile(filepath.Join(s.Test.Artifacts, rel))
		if err != nil {
			m.log.Warn("acp: cannot read test screenshot", "session", s.ID, "file", rel, "err", err)
			continue
		}
		name := filepath.Base(rel)
		mimeType := mime.TypeByExtension(filepath.Ext(name))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = m.writer.AttachFile(ctx, s.CardID, name, mimeType, data)
		cancel()
		if err != nil {
			m.log.Warn("acp: cannot attach test screenshot", "session", s.ID, "file", rel, "err", err)
		}
	}
}

// moveAfterTest advances the card according to the verdict. A blocked run and
// an unconfigured column both leave the card where it is: there is nothing to
// say beyond the comment.
func (m *Manager) moveAfterTest(s *Session, res webtest.Result) {
	m.cfgMu.RLock()
	property, pass, fail := m.cfg.TriggerProperty, m.cfg.TestPassColumn, m.cfg.TestFailColumn
	m.cfgMu.RUnlock()

	var column string
	switch res.Verdict {
	case webtest.VerdictPass:
		column = strings.TrimSpace(pass)
	case webtest.VerdictFail:
		column = strings.TrimSpace(fail)
	}
	if column == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.writer.MoveCardByOptionName(ctx, s.CardID, property, column); err != nil {
		m.log.Warn("acp: cannot move card after test", "card", s.CardID, "column", column, "err", err)
		m.comment(s, fmt.Sprintf("Не удалось перевести карточку в колонку «%s»: %v", column, err))
	}
}
