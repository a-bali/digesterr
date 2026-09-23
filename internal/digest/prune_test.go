package digest

import (
	"encoding/json"
	"testing"
)

// configWithEverything is a fully-populated digest config: every field the old
// prune implementation was capable of destroying.
const configWithEverything = `{
  "categories": [{"name":"Tech","highlights":2,"comments":3}],
  "feeds": [{"id":11,"highlights":4,"comments":6},{"id":12,"highlights":1,"comments":1}],
  "default_highlights": 7,
  "default_comments": 8,
  "max_items": 42,
  "min_score": 25,
  "prompt": "Summarise for a busy engineer."
}`

// decoded is the set of top-level keys a rewrite must preserve byte-for-byte.
func decoded(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return m
}

// TestPruneSourcesPreservesEveryOtherField is the regression test for the
// settings-erasure bug: the rewrite must not round-trip through a struct that
// only knows about feeds and categories.
func TestPruneSourcesPreservesEveryOtherField(t *testing.T) {
	out, changed, err := PruneSources(configWithEverything, []int64{12}, "", false)
	if err != nil {
		t.Fatalf("PruneSources: %v", err)
	}
	if !changed {
		t.Fatal("expected the feed to be pruned")
	}

	before := decoded(t, configWithEverything)
	after := decoded(t, out)
	for _, key := range []string{
		"default_highlights", "default_comments", "max_items", "min_score", "prompt",
	} {
		got, ok := after[key]
		if !ok {
			t.Errorf("%s was dropped by the prune", key)
			continue
		}
		if string(got) != string(before[key]) {
			t.Errorf("%s changed: %s -> %s", key, before[key], got)
		}
	}
	// The selected feed is gone and the kept one survives with its own limits.
	var feeds []digestFeedSelection
	if err := json.Unmarshal(after["feeds"], &feeds); err != nil {
		t.Fatalf("decode feeds: %v", err)
	}
	if len(feeds) != 1 || feeds[0].ID != 11 {
		t.Errorf("feeds after prune = %+v, want only id 11", feeds)
	}
	if feeds[0].Highlights != 4 || feeds[0].Comments != 6 {
		t.Errorf("kept feed lost its per-source limits: %+v", feeds[0])
	}
	// The unrelated category selection is untouched.
	var cats []digestCategorySelection
	if err := json.Unmarshal(after["categories"], &cats); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if len(cats) != 1 || cats[0].Name != "Tech" {
		t.Errorf("categories after prune = %+v, want Tech untouched", cats)
	}
}

// TestPruneSourcesKeepsCategoryWhileOtherFeedsRemain is the regression test for
// the over-eager category prune: a selection names the CATEGORY, so one feed
// leaving it must not drop the selection while other feeds are still in it. It
// also pins the broadening refusal for the last-feed case.
func TestPruneSourcesKeepsCategoryWhileOtherFeedsRemain(t *testing.T) {
	cfg := `{"categories":[{"name":"Tech","highlights":2,"comments":3}],"max_items":10}`

	out, changed, err := PruneSources(cfg, nil, "Tech", false)
	if err != nil {
		t.Fatalf("PruneSources: %v", err)
	}
	if changed {
		t.Errorf("category was pruned while feeds remained in it; got %s", out)
	}
	if out != cfg {
		t.Errorf("config changed unexpectedly:\n got %s\nwant %s", out, cfg)
	}

	// When it IS the last feed, the category selection would be dropped — but
	// that would leave the config source-less, which the generator reads as "every
	// feed". The prune therefore refuses and reports no change, leaving the stale
	// selector in place: it resolves to nothing, which is safer than widening.
	out, changed, err = PruneSources(cfg, nil, "Tech", true)
	if err != nil {
		t.Fatalf("PruneSources: %v", err)
	}
	if changed {
		t.Errorf("pruning the last source must be refused (it would mean ALL feeds); got %s", out)
	}
	if out != cfg {
		t.Errorf("config modified:\n got %s\nwant %s", out, cfg)
	}
}

// TestPruneSourcesNeverEmptiesAConfig is the regression test for the broadening
// bug itself. The generator reads "no feeds and no categories" as "every feed
// the user owns", so pruning the LAST source would silently widen a scheduled
// digest to the whole account. The prune must refuse and leave the config alone.
func TestPruneSourcesNeverEmptiesAConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		ids  []int64
	}{
		{
			name: "last selected feed deleted",
			cfg:  `{"feeds":[{"id":5,"highlights":1,"comments":1}],"prompt":"keep me"}`,
			ids:  []int64{5},
		},
		{
			name: "only feed is one of several deleted",
			cfg:  `{"feeds":[{"id":5,"highlights":1,"comments":1},{"id":6,"highlights":1,"comments":1}]}`,
			ids:  []int64{5, 6},
		},
		{
			name: "all selected categories emptied",
			cfg:  `{"categories":[{"name":"A","highlights":1,"comments":1}]}`,
			ids:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := ""
			catEmpty := false
			if len(tc.ids) == 0 {
				cat, catEmpty = "A", true
			}
			out, changed, err := PruneSources(tc.cfg, tc.ids, cat, catEmpty)
			if err != nil {
				t.Fatalf("PruneSources: %v", err)
			}
			if changed {
				t.Errorf("prune emptied the config; a source-less config means ALL feeds: %s", out)
			}
			if out != tc.cfg {
				t.Errorf("config modified:\n got %s\nwant %s", out, tc.cfg)
			}
		})
	}
}

// TestPruneSourcesLeavesUnrelatedConfigsAlone: a config selecting neither the
// deleted feed nor the emptied category is not rewritten at all. Rewriting it
// would be a pointless write (and a chance to lose fields).
func TestPruneSourcesLeavesUnrelatedConfigsAlone(t *testing.T) {
	cfg := `{"feeds":[{"id":99,"highlights":1,"comments":1}],"categories":[{"name":"Other","highlights":1,"comments":1}],"max_items":5}`
	out, changed, err := PruneSources(cfg, []int64{1, 2}, "Tech", true)
	if err != nil {
		t.Fatalf("PruneSources: %v", err)
	}
	if changed {
		t.Errorf("unrelated config was rewritten: %s", out)
	}
	if out != cfg {
		t.Errorf("config modified:\n got %s\nwant %s", out, cfg)
	}
}

// TestPruneSourcesKeepsTheOtherList: a config with BOTH lists where only one is
// emptied must keep the other, so it does not silently become source-less.
func TestPruneSourcesKeepsTheOtherList(t *testing.T) {
	cfg := `{"feeds":[{"id":5,"highlights":1,"comments":1}],"categories":[{"name":"Tech","highlights":1,"comments":1}]}`
	// Delete the feed, leave the category populated: the config still selects Tech.
	out, changed, err := PruneSources(cfg, []int64{5}, "", false)
	if err != nil {
		t.Fatalf("PruneSources: %v", err)
	}
	if !changed {
		t.Fatal("expected the feed to be pruned")
	}
	after := decoded(t, out)
	var feeds []digestFeedSelection
	if err := json.Unmarshal(after["feeds"], &feeds); err != nil {
		t.Fatalf("decode feeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("feeds = %+v, want empty", feeds)
	}
	var cats []digestCategorySelection
	if err := json.Unmarshal(after["categories"], &cats); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if len(cats) != 1 {
		t.Errorf("categories = %+v, want Tech kept", cats)
	}
}

// TestPruneSourcesRejectsMalformedJSON: an unparseable config must surface as an
// error so the caller can skip it, never as a silently emptied config.
func TestPruneSourcesRejectsMalformedJSON(t *testing.T) {
	if _, _, err := PruneSources(`{"feeds":`, []int64{1}, "", false); err == nil {
		t.Error("expected an error for malformed JSON")
	}
	// A JSON array (not an object) is also not a config.
	if _, _, err := PruneSources(`[1,2,3]`, []int64{1}, "", false); err == nil {
		t.Error("expected an error for a non-object config")
	}
}
