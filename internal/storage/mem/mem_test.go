package mem

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yuandzhang/webhook-zq/internal/storage"
)

func seed(t *testing.T, retentionEvents, retentionDays int) (*Store, uuid.UUID) {
	t.Helper()

	store := New()

	inbox := storage.Inbox{
		ID:                 uuid.New(),
		Name:               "t",
		Token:              "token",
		Enabled:            true,
		RetentionMaxEvents: retentionEvents,
		RetentionMaxDays:   retentionDays,
	}

	if err := store.CreateInbox(context.Background(), &inbox); err != nil {
		t.Fatal(err)
	}

	return store, inbox.ID
}

// TestBinaryBodyIsNotSearchable pins the rule that both drivers share: a payload that is
// not representable as text has no searchable mirror, so keyword search must not find it.
//
// This test is the unit level counterpart of the PostgreSQL regression: if the in-memory
// driver searched the raw body instead, this bug would silently come back.
func TestBinaryBodyIsNotSearchable(t *testing.T) {
	t.Parallel()

	store, inboxID := seed(t, 0, 0)

	ev := storage.Event{
		ID:          uuid.New(),
		InboxID:     inboxID,
		Method:      "POST",
		ContentType: "application/json",
		Body:        []byte{0xff, 0x00, 'a'},
	}

	if err := store.CreateEvent(context.Background(), &ev); err != nil {
		t.Fatal(err)
	}

	if ev.BodyText != "" {
		t.Fatalf("binary body must not produce a text mirror, got %q", ev.BodyText)
	}

	items, total, err := store.ListEvents(context.Background(), storage.EventFilter{InboxID: inboxID, Query: "a"})
	if err != nil {
		t.Fatal(err)
	}

	if total != 0 || len(items) != 0 {
		t.Fatalf("binary payloads must not be searchable, got %d", total)
	}

	// ...but the event is still there and can be opened/replayed.
	all, _, err := store.ListEvents(context.Background(), storage.EventFilter{InboxID: inboxID})
	if err != nil || len(all) != 1 {
		t.Fatalf("the event must still be listed, got %d %v", len(all), err)
	}
}

func TestTextBodyIsSearchable(t *testing.T) {
	t.Parallel()

	store, inboxID := seed(t, 0, 0)

	ev := storage.Event{
		ID:          uuid.New(),
		InboxID:     inboxID,
		Method:      "POST",
		ContentType: "application/json; charset=utf-8",
		Body:        []byte(`{"event":"order.paid"}`),
	}

	if err := store.CreateEvent(context.Background(), &ev); err != nil {
		t.Fatal(err)
	}

	items, total, err := store.ListEvents(context.Background(), storage.EventFilter{
		InboxID:     inboxID,
		Query:       "order",
		ContentType: "application/json", // media type only, without parameters
	})
	if err != nil {
		t.Fatal(err)
	}

	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 match, got %d", total)
	}

	if items[0].Preview == "" {
		t.Fatal("the list must carry a preview")
	}
}

func TestPruneEvents(t *testing.T) {
	t.Parallel()

	store, inboxID := seed(t, 3, 30)

	for i := 0; i < 5; i++ {
		ev := storage.Event{ID: uuid.New(), InboxID: inboxID, Method: "POST", Body: []byte("x")}
		if err := store.CreateEvent(context.Background(), &ev); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.PruneEvents(context.Background(), inboxID, 3, 30)
	if err != nil {
		t.Fatal(err)
	}

	if deleted != 2 {
		t.Fatalf("expected 2 events to be pruned, got %d", deleted)
	}

	_, total, err := store.ListEvents(context.Background(), storage.EventFilter{InboxID: inboxID})
	if err != nil {
		t.Fatal(err)
	}

	if total != 3 {
		t.Fatalf("expected 3 events to remain, got %d", total)
	}
}
