package digest

import (
	"encoding/json"
	"fmt"
)

// PruneSources removes feed IDs — and, when it has become empty, a selected
// category — from a digest config's source lists. It returns the rewritten
// config JSON and whether anything changed.
//
// Two properties this function exists to guarantee:
//
//  1. **Every other field survives.** The rewrite is a top-level key
//     substitution over `json.RawMessage`, so `prompt`, `min_score`, `max_items`,
//     `default_highlights`, `default_comments` and any key added in future pass
//     through byte-for-byte. An earlier revision round-tripped the config through
//     a struct holding only feeds and categories, which silently erased all of
//     them on any prune — a feed deletion quietly reset the user's prompt and
//     scoring threshold.
//
//  2. **A prune never broadens the config.** The generator treats a config with
//     NO sources as "every feed the user owns" (that is the documented meaning of
//     an empty selection in the UI). So pruning the last remaining source would
//     silently widen a scheduled digest to the whole account. When the pruned
//     result would have no sources at all, the original config is returned
//     unchanged: the stale selector resolves to nothing and the digest generates
//     nothing until the user reconfigures, which is the documented intent and
//     strictly safer than mailing the user every feed.
//
// categoryEmpty must be true only when the named category still exists but holds
// no feeds. A category is a live reference to a NAME, so one feed leaving it does
// not invalidate the selection while other feeds remain in it; pruning on every
// departure made a config stop tracking a category the user never edited.
func PruneSources(raw string, feedIDs []int64, category string, categoryEmpty bool) (string, bool, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return raw, false, fmt.Errorf("parsing digest config: %w", err)
	}
	if top == nil {
		return raw, false, nil
	}

	changed := false

	if len(feedIDs) > 0 {
		feeds, err := decodeFeedSelections(top["feeds"])
		if err != nil {
			return raw, false, err
		}
		drop := make(map[int64]bool, len(feedIDs))
		for _, id := range feedIDs {
			drop[id] = true
		}
		kept := make([]digestFeedSelection, 0, len(feeds))
		for _, f := range feeds {
			if drop[int64(f.ID)] {
				changed = true
				continue
			}
			kept = append(kept, f)
		}
		if changed {
			blob, err := json.Marshal(kept)
			if err != nil {
				return raw, false, fmt.Errorf("encoding feeds: %w", err)
			}
			top["feeds"] = blob
		}
	}

	if category != "" && categoryEmpty {
		cats, err := decodeCategorySelections(top["categories"])
		if err != nil {
			return raw, false, err
		}
		kept := make([]digestCategorySelection, 0, len(cats))
		catsChanged := false
		for _, c := range cats {
			if c.Name == category {
				catsChanged = true
				continue
			}
			kept = append(kept, c)
		}
		if catsChanged {
			blob, err := json.Marshal(kept)
			if err != nil {
				return raw, false, fmt.Errorf("encoding categories: %w", err)
			}
			top["categories"] = blob
			changed = true
		}
	}

	if !changed {
		return raw, false, nil
	}
	// The broadening guard, applied to the CANDIDATE rather than to each list:
	// only the combined result decides whether this config still selects anything.
	if !hasAnySource(top) {
		return raw, false, nil
	}
	blob, err := json.Marshal(top)
	if err != nil {
		return raw, false, fmt.Errorf("encoding digest config: %w", err)
	}
	return string(blob), true, nil
}

// hasAnySource reports whether either source list holds at least one entry.
// A missing key is the same as an empty list — the generator only distinguishes
// "both empty" (all feeds) from "anything selected".
func hasAnySource(top map[string]json.RawMessage) bool {
	feeds, err := decodeFeedSelections(top["feeds"])
	if err == nil && len(feeds) > 0 {
		return true
	}
	cats, err := decodeCategorySelections(top["categories"])
	return err == nil && len(cats) > 0
}

// decodeFeedSelections reads the feed list, treating an absent key as empty.
func decodeFeedSelections(raw json.RawMessage) ([]digestFeedSelection, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var sel []digestFeedSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("parsing feeds: %w", err)
	}
	return sel, nil
}

// decodeCategorySelections reads the category list, treating an absent key as empty.
func decodeCategorySelections(raw json.RawMessage) ([]digestCategorySelection, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var sel []digestCategorySelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("parsing categories: %w", err)
	}
	return sel, nil
}
