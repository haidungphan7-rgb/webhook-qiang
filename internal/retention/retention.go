// Package retention enforces the per-inbox retention policy in the background.
//
// Without it the retention settings would be a promise that is never kept: events are up
// to 1 MiB each, so an inbox left alone would grow without bound until the disk is full.
// The limits live on the inbox itself (retention_max_events / retention_max_days), so a
// busy inbox can be configured differently from a quiet one.
package retention

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// Start runs the cleaner on a ticker until the context is cancelled.
func Start(ctx context.Context, log *zap.Logger, store storage.Store, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				RunOnce(ctx, log, store)
			}
		}
	}()
}

// RunOnce walks every inbox and prunes its events. Errors are logged and skipped: a
// single broken inbox must not stop the rest from being cleaned.
func RunOnce(ctx context.Context, log *zap.Logger, store storage.Store) int64 {
	const pageSize = 200

	var (
		offset int64
		total  int64
	)

	for {
		// AllOwners: the cleaner maintains every tenant's retention limits, and the
		// default tenant filter would otherwise silently reduce this to zero inboxes
		// (owner_key defaults to 'default', not to the empty string).
		inboxes, _, err := store.ListInboxes(ctx, storage.InboxFilter{
			AllOwners: true,
			Limit:     pageSize,
			Offset:    int(offset),
		})
		if err != nil {
			log.Error("retention: cannot list inboxes", zap.Error(err))

			return total
		}

		if len(inboxes) == 0 {
			return total
		}

		for _, in := range inboxes {
			deleted, err := store.PruneEvents(ctx, in.ID, in.RetentionMaxEvents, in.RetentionMaxDays)
			if err != nil {
				log.Error("retention: cannot prune events",
					zap.String("inbox_id", in.ID.String()), zap.Error(err))

				continue
			}

			if deleted > 0 {
				total += deleted

				log.Info("retention: pruned events",
					zap.String("inbox_id", in.ID.String()),
					zap.Int64("deleted", deleted))
			}
		}

		if len(inboxes) < pageSize {
			return total
		}

		offset += int64(len(inboxes))
	}
}
