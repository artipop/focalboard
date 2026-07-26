package acp

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "acp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStoreSessionRoundTrip(t *testing.T) {
	st := openTestStore(t)
	rec := SessionRecord{
		ID: "s1", CardID: "c1", BoardID: "b1", AgentKind: "claude",
		Status: StatusQueued, StartedAt: time.Now(),
	}
	if err := st.InsertSession(rec); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent("s1", 1, "chunk", map[string]any{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionStatus("s1", StatusDone, ""); err != nil {
		t.Fatal(err)
	}

	sessions, events, err := st.SessionsForCard("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Status != StatusDone || sessions[0].FinishedAt == nil {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	if len(events) != 1 || events[0].Kind != "chunk" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestStoreStaleSessions(t *testing.T) {
	st := openTestStore(t)
	for id, status := range map[string]SessionStatus{
		"q": StatusQueued, "r": StatusRunning, "w": StatusWaitingPermission, "d": StatusDone,
	} {
		if err := st.InsertSession(SessionRecord{ID: id, CardID: "c", BoardID: "b", AgentKind: "claude", Status: status, StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := st.StaleSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 3 {
		t.Fatalf("expected 3 stale sessions, got %d", len(stale))
	}
}

func TestClaimIdempotency(t *testing.T) {
	st := openTestStore(t)
	window := 10 * time.Second

	fresh, err := st.ClaimIdempotency("k1", "s1", window)
	if err != nil || !fresh {
		t.Fatalf("first claim: fresh=%v err=%v", fresh, err)
	}
	fresh, err = st.ClaimIdempotency("k1", "s2", window)
	if err != nil || fresh {
		t.Fatalf("duplicate claim within window must not be fresh: fresh=%v err=%v", fresh, err)
	}
	// A different key is independent.
	fresh, err = st.ClaimIdempotency("k2", "s3", window)
	if err != nil || !fresh {
		t.Fatalf("independent key: fresh=%v err=%v", fresh, err)
	}
	// Expired keys can be claimed again.
	fresh, err = st.ClaimIdempotency("k1", "s4", -time.Second)
	if err != nil || !fresh {
		t.Fatalf("expired key should be claimable: fresh=%v err=%v", fresh, err)
	}
}

func TestOpenStoreIdempotentMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acp.db")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenStore(path)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	st.Close()
}
