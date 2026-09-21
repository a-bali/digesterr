package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

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

	if removed, err := s.pruneDigestConfigSources(r.Context(), userID, []int64{feed.ID}, feed.Category); err != nil {
		slog.Warn("failed to prune digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "error", err)
	} else if removed > 0 {
		slog.Info("pruned digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "configs_updated", removed)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
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

// pruneDigestConfigSources removes the given feed IDs and category from every
// digest config of the user that references them. A config left with no
// sources at all gets both lists cleared (it stays but generates nothing
// until reconfigured). Returns the number of configs updated.
func (s *Server) pruneDigestConfigSources(ctx context.Context, userID int64, feedIDs []int64, category string) (int, error) {
	configs, err := s.queries.ListDigestConfigs(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("listing digest configs: %w", err)
	}
	idSet := make(map[int64]bool, len(feedIDs))
	for _, fid := range feedIDs {
		idSet[fid] = true
	}
	updated := 0
	for _, cfg := range configs {
		var parsed struct {
			Feeds []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			} `json:"feeds"`
			Categories []struct {
				Name       string `json:"name"`
				Highlights int    `json:"highlights"`
				Comments   int    `json:"comments"`
			} `json:"categories"`
		}
		if err := json.Unmarshal([]byte(cfg.Config), &parsed); err != nil {
			continue // unparseable config — leave untouched
		}
		feedsChanged, catsChanged := false, false
		feeds := parsed.Feeds[:0]
		for _, f := range parsed.Feeds {
			if idSet[int64(f.ID)] {
				feedsChanged = true
				continue
			}
			feeds = append(feeds, f)
		}
		categories := parsed.Categories[:0]
		for _, c := range parsed.Categories {
			if category != "" && c.Name == category {
				catsChanged = true
				continue
			}
			categories = append(categories, c)
		}
		if !feedsChanged && !catsChanged {
			continue
		}
		parsed.Feeds = feeds
		parsed.Categories = categories
		blob, err := json.Marshal(parsed)
		if err != nil {
			return updated, fmt.Errorf("marshaling config %d: %w", cfg.ID, err)
		}
		if _, err := s.queries.UpdateDigestConfig(ctx, store.UpdateDigestConfigParams{
			ID:       cfg.ID,
			UserID:   userID,
			Name:     cfg.Name,
			Schedule: cfg.Schedule,
			Config:   string(blob),
		}); err != nil {
			return updated, fmt.Errorf("updating config %d: %w", cfg.ID, err)
		}
		updated++
	}
	return updated, nil
}
