package retention

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

// TestRunOnce_SweepsEveryTenant guards the owner filter: the cleaner maintains retention
// for all tenants, so an inbox owned by "alice" must be pruned even though the caller
// does not know (and must not need to know) the tenant list.
func TestRunOnce_SweepsEveryTenant(t *testing.T) {
	t.Parallel()

	store := mem.New()
	ctx := context.Background()

	in := storage.Inbox{
		ID: uuid.New(), OwnerKey: "alice", Name: "alice-inbox", Token: "d",
		Enabled: true, RetentionMaxEvents: 1, RetentionMaxDays: 0,
	}

	if err := store.CreateInbox(ctx, &in); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		ev := storage.Event{ID: uuid.New(), InboxID: in.ID, Method: "POST", Body: []byte("x")}

		if err := store.CreateEvent(ctx, &ev); err != nil {
			t.Fatal(err)
		}
	}

	// Sanity check: the tenant filter itself still works, so the inbox really is
	// invisible to a plain (tenant scoped) listing.
	listed, _, err := store.ListInboxes(ctx, storage.InboxFilter{OwnerKey: "bob"})
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 0 {
		t.Fatalf("tenant filter is broken: bob must not see alice's inbox, got %d", len(listed))
	}

	if deleted := RunOnce(ctx, zap.NewNop(), store); deleted != 3 {
		t.Fatalf("expected 3 events pruned for a non-default tenant, got %d", deleted)
	}

	_, total, err := store.ListEvents(ctx, storage.EventFilter{InboxID: in.ID})
	if err != nil || total != 1 {
		t.Fatalf("expected 1 remaining event, got %d (%v)", total, err)
	}
}

// TestRunOnce_UsesPerInboxLimits verifies that retention is driven by the limits stored
// on each inbox, not by a global setting: two inboxes with different limits keep a
// different number of events.
func TestRunOnce_UsesPerInboxLimits(t *testing.T) {
	t.Parallel()

	store := mem.New()

	// OwnerKey is set on purpose: real rows carry 'default' (or a tenant name), and the
	// cleaner must sweep them without asking for a specific tenant. An earlier version
	// listed with an empty owner, matched nothing on PostgreSQL and pruned nothing at all.
	small := storage.Inbox{ID: uuid.New(), OwnerKey: "default", Name: "small", Token: "a", Enabled: true, RetentionMaxEvents: 2, RetentionMaxDays: 0}
	big := storage.Inbox{ID: uuid.New(), OwnerKey: "default", Name: "big", Token: "b", Enabled: true, RetentionMaxEvents: 10, RetentionMaxDays: 0}
	old := storage.Inbox{ID: uuid.New(), OwnerKey: "default", Name: "old", Token: "c", Enabled: true, RetentionMaxEvents: 0, RetentionMaxDays: 7}

	for _, in := range []storage.Inbox{small, big, old} {
		if err := store.CreateInbox(context.Background(), &in); err != nil {
			t.Fatal(err)
		}
	}

	add := func(inboxID uuid.UUID, n int, age time.Duration) {
		for i := 0; i < n; i++ {
			ev := storage.Event{
				ID:        uuid.New(),
				InboxID:   inboxID,
				Method:    "POST",
				Body:      []byte("x"),
				CreatedAt: time.Now().UTC().Add(-age),
			}

			if err := store.CreateEvent(context.Background(), &ev); err != nil {
				t.Fatal(err)
			}
		}
	}

	add(small.ID, 5, 0)          // keeps 2
	add(big.ID, 3, 0)            // keeps 3
	add(old.ID, 2, 10*24*time.Hour) // all older than 7 days -> removed

	deleted := RunOnce(context.Background(), zap.NewNop(), store)

	if deleted != 5 { // 3 from small + 2 from old
		t.Fatalf("expected 5 events pruned, got %d", deleted)
	}

	count := func(inboxID uuid.UUID) int {
		_, total, err := store.ListEvents(context.Background(), storage.EventFilter{InboxID: inboxID})
		if err != nil {
			t.Fatal(err)
		}

		return total
	}

	if got := count(small.ID); got != 2 {
		t.Errorf("small inbox should keep 2 events, got %d", got)
	}

	if got := count(big.ID); got != 3 {
		t.Errorf("big inbox should keep 3 events, got %d", got)
	}

	if got := count(old.ID); got != 0 {
		t.Errorf("expired events should be removed, got %d", got)
	}
}

// TestRunOnce_NoInboxes makes sure the cleaner is a no-op on an empty database instead of
// logging errors or crashing.
func TestRunOnce_NoInboxes(t *testing.T) {
	t.Parallel()

	if got := RunOnce(context.Background(), zap.NewNop(), mem.New()); got != 0 {
		t.Fatalf("expected 0 deletions, got %d", got)
	}
}
