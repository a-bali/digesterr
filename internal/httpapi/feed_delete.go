package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"digesterr/internal/digest"
	"digesterr/internal/store"
)

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Capture the feed before deletion so digest configs referencing it (by
	// feed ID or by its category) can be cleaned up afterwards.
	feed, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{ID: id, UserID: userID})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}

	// Deleting an archive discards it for good: its items are already
	// orphans of a previous deletion, and the archive exists only to hold
	// them. Deleting a live feed re-homes its kept items (saved or
	// digest-linked) to a new archive feed instead, so they survive.
	if feed.IsArchive != 0 {
		if err := s.deleteArchiveFeed(r.Context(), feed, userID); err != nil {
			writeServerError(w, "failed to delete archive", err)
			return
		}
	} else if err := s.deleteFeedWithArchive(r.Context(), feed, userID); err != nil {
		writeServerError(w, "failed to delete feed", err)
		return
	}

	// Count the category's remaining feeds AFTER the delete: the category is
	// pruned only when this reaches zero, so a feed leaving a shared category
	// does not drop the selection for the feeds still in it.
	if removed, err := s.pruneDigestConfigSources(r.Context(), userID, []int64{feed.ID}, feed.Category, s.feedsLeftInCategory(r.Context(), userID, feed.Category)); err != nil {
		slog.Warn("failed to prune digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "error", err)
	} else if removed > 0 {
		slog.Info("pruned digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "configs_updated", removed)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// feedsLeftInCategory reports how many non-archive feeds the user still has in
// the named category. A prune only removes a category selection when this is
// zero: the selection names the CATEGORY, so it stays meaningful while any feed
// remains in it. An empty name is not a category at all (it is "uncategorised"),
// so it always reports zero.
func (s *Server) feedsLeftInCategory(ctx context.Context, userID int64, category string) int {
	if category == "" {
		return 0
	}
	feeds, err := s.queries.ListFeedsByCategory(ctx, store.ListFeedsByCategoryParams{
		Category: category,
		UserID:   userID,
	})
	if err != nil {
		// A failed lookup must not prune: treating "unknown" as "empty" would
		// drop a live category selection on a transient DB error.
		slog.Warn("failed to count feeds left in category", "category", category, "error", err)
		return 1
	}
	return len(feeds)
}

// deleteArchiveFeed permanently discards an archive feed and everything in
// it. Archives hold only orphaned items of previously deleted feeds, so there
// is nothing to re-home: the user is explicitly throwing the keepsakes away.
func (s *Server) deleteArchiveFeed(ctx context.Context, feed store.Feed, userID int64) error {
	deleted, err := s.queries.DeleteFeed(ctx, store.DeleteFeedParams{ID: feed.ID, UserID: userID})
	if err != nil {
		return fmt.Errorf("deleting archive feed %d: %w", feed.ID, err)
	}
	if deleted == 0 {
		return fmt.Errorf("archive feed %d not found", feed.ID)
	}
	return nil
}

// deleteFeedWithArchive deletes a live feed while preserving its kept items
// (saved or digest-linked) in a new archive feed named "Saved — <name>".
// Everything runs in one transaction: a partial failure must not leave items
// re-homed to an archive whose source feed still exists, or vice versa.
func (s *Server) deleteFeedWithArchive(ctx context.Context, feed store.Feed, userID int64) error {
	db := s.queries.DB()
	if db == nil {
		return fmt.Errorf("feed deletion requires a *sql.DB-backed Queries")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning feed deletion: %w", err)
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)

	archive, err := qtx.CreateArchiveFeed(ctx, store.CreateArchiveFeedParams{
		UserID:   userID,
		Category: feed.Category,
		Name:     "Saved -- " + feed.Name,
	})
	if err != nil {
		return fmt.Errorf("creating archive feed: %w", err)
	}
	if _, err := qtx.RehomeFeedItems(ctx, store.RehomeFeedItemsParams{
		NewFeedID: archive.ID,
		OldFeedID: feed.ID,
	}); err != nil {
		return fmt.Errorf("rehoming kept items: %w", err)
	}
	if _, err := qtx.DeleteUnkeptFeedItems(ctx, feed.ID); err != nil {
		return fmt.Errorf("deleting unkept items: %w", err)
	}
	deleted, err := qtx.DeleteFeed(ctx, store.DeleteFeedParams{ID: feed.ID, UserID: userID})
	if err != nil {
		return fmt.Errorf("deleting feed %d: %w", feed.ID, err)
	}
	if deleted == 0 {
		return fmt.Errorf("feed %d not found", feed.ID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing feed deletion: %w", err)
	}
	slog.Info("feed deleted with archive", "feed_id", feed.ID, "archive_id", archive.ID)
	return nil
}

// pruneDigestConfigSources removes the given feed IDs and, when it no longer
// holds any feeds, the given category from every digest config of the user that
// references them. Returns the number of configs updated.
//
// The category is pruned ONLY when it is empty (see digest.PruneSources). It is
// a live reference to a name, so a feed leaving it does not invalidate the
// selection while other feeds remain — pruning on every departure silently
// dropped a source the user never touched. Pass the category's remaining feed
// count as categoryFeedsLeft.
//
// A config left with no sources at all is deliberately left ALONE: the generator
// reads "no sources" as "all feeds", so clearing the last source would widen the
// digest to the whole account.
func (s *Server) pruneDigestConfigSources(ctx context.Context, userID int64, feedIDs []int64, category string, categoryFeedsLeft int) (int, error) {
	configs, err := s.queries.ListDigestConfigs(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("listing digest configs: %w", err)
	}
	updated := 0
	for _, cfg := range configs {
		rewritten, changed, err := digest.PruneSources(cfg.Config, feedIDs, category, categoryFeedsLeft == 0)
		if err != nil {
			// Unparseable config — leave it untouched rather than risk destroying it.
			slog.Warn("skipping digest config with unparseable config JSON",
				"config_id", cfg.ID, "error", err)
			continue
		}
		if !changed {
			continue
		}
		if _, err := s.queries.UpdateDigestConfig(ctx, store.UpdateDigestConfigParams{
			ID:       cfg.ID,
			UserID:   userID,
			Name:     cfg.Name,
			Schedule: cfg.Schedule,
			Config:   rewritten,
		}); err != nil {
			return updated, fmt.Errorf("updating config %d: %w", cfg.ID, err)
		}
		updated++
	}
	return updated, nil
}
