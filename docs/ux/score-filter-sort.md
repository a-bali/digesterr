# UX Plan — Score-based Filtering & Sorting

Task: t_ed1a32ec (parent of the implementation tasks: feed config min score, digest config min score, feed item view filter/sort UI, digest view filter/sort UI).

Status: **design APPROVED (Variant A) with revisions on 2026-08-15 — mockups regenerated, awaiting final confirmation. No production code changed.**

---

## 1. Current state (reviewed in code, 2026-08-15)

| Surface | File | What exists today |
|---|---|---|
| Feed config editor | `web/src/components/FeedDialog.vue` | Category, Type, Name, URL, "Auto-extract full article content" toggle. No score field. |
| Digest config editor | `web/src/components/DigestDialog.vue` | Name, Schedule, Source (categories/feeds), Max highlights/comments/items, Prompt. No score field. |
| Feed item middle pane | `web/src/components/ItemList.vue` + `FilterBar.vue` | Segmented buttons All / Unread / Read / Digested / Saved. Server-side status filter, fixed ordering `published_at DESC` (see `sql/queries/items.sql`). No sort control. |
| Digest middle pane | `web/src/components/DigestResultList.vue` + `FilterBar.vue` | Segmented buttons All / Unread / Read. Client-side status filter, order `created_at DESC`. No sort control. |
| FilterBar | `web/src/components/FilterBar.vue` | Shared component; collapses to a `<select>` below 400 px pane width via ResizeObserver. |
| Data model | `web/src/types/api.ts`, `internal/schema/schema.sql` | Items carry `points` + `comments_count` (default 0). No separate "score" column. |

## 2. Score semantics (what "score" means here)

- **Score = the item's `points` value** — HN score, Lobste.rs points, Reddit upvotes. It is already displayed in the item list with the ↑ icon (`ItemList.vue`).
- **Score-capable feed types:** `hn`, `lobsters`, `reddit` (enrichers populate `points`). Plain RSS/Atom feeds have no score data (points stay 0).
- **Detection rule (client):** a feed supports scores iff `feed.type ∈ {hn, lobsters, reddit}`. The **Saved items** view mixes feeds of both kinds, so score filter/sort is unavailable there (options greyed out, same treatment as an unsupported feed).
- **Threshold rule (shared by feed ingestion and digest generation):** the configured minimum score applies **only to items that have score data**. Items without a score (RSS items) are never blocked by the threshold. This matches the parent task's instruction and avoids silently starving RSS feeds.
- **Refresh rule:** scores rise over time (an HN story gains points), so thresholds are re-evaluated on every fetch (feed ingestion) and at generation time (digests), using freshly enriched scores.

## 3. Required behavior (from ticket t_6cdeb0e3)

1. Feed config: minimum score — items below it are not inserted; rechecked every fetch because scores increase.
2. Digest config: minimum score — items below it are excluded from the digest.
3. Feed item view: replace the filter bar with a **filter icon** (status + score filtering) and a **sort icon** (date asc/desc, score asc/desc); **score options greyed out when the feed doesn't support scores**.
4. Digest view: filter/sort icons limited to **status filtering** and **date sorting** — no score options at all.
5. No production code changes in this planning task.

## 4. Approved design — Variant A "Slim toolbar" (revised per user feedback 2026-08-15)

Variants B ("pane header") and C ("state chips") were offered in the first review round and **declined**; their mockups remain in `sketches/score-filter-sort/{variant-b-pane-header,variant-c-state-chips}/` for reference.

### 4.1 Toolbar — two icons only

The existing filter row keeps its footprint (`p-2 border-b`). It contains **exactly two ghost icon buttons**:

- **Filter** (funnel icon) — opens the filter dropdown.
- **Sort** (sort glyph) — opens the sort dropdown.

Removed from the bar: the segmented feed tabs, the state label (e.g. "Newest first"), and the reset element. All state is encoded in the icons (below). The previous `FilterBar.vue` collapse logic is no longer needed — two 24 px icons always fit any pane width the app supports.

### 4.2 Filter icon state indicators

- **Active status filter:** a small **status glyph** is rendered right after the funnel icon: `Layers` (All), `CircleDot` (Unread), `CircleCheck` (Read), `Sparkles` (Digested), `Bookmark` (Saved) — the same glyphs already used by the app's status icons. The glyph is accent-colored when the status filter is non-default (anything other than All).
- **Active score filter:** the funnel icon itself turns **accent-colored and shows a small dot** at its top-right corner whenever a min-score threshold is set.

### 4.3 Filter dropdown (reka-ui `DropdownMenu`, anchored right)

- **Status section:** All / Unread / Read / Digested / Saved — check on the active item.
- Divider.
- **Score section — numeric min-score input (not preset tiers):**
  - A small number input (`min=0`, empty/0 = any score) with a **Clear** affordance ("Clear" appears when a threshold is set; otherwise a muted "Any" label).
  - Helper line: "Items below N points are hidden. Leave 0 for any score."
- **Unsupported contexts** (feed type without scores, or the Saved view): the input is **disabled + greyed** and the note "Scores not available for this feed." / "…for saved items (feeds are mixed)." is shown. Score options are **greyed, not hidden** — the section remains visible so users understand why it is unavailable.

### 4.4 Sort dropdown

- **Date:** Newest first (date ↓, default) / Oldest first (date ↑).
- Divider.
- **Score:** Highest first (score ↓) / Lowest first (score ↑) — explicit ascending/descending for both dimensions.
- Unsupported contexts: the Score section items are **disabled + greyed** with the same note as the filter menu. The digest view has **no score section at all** (status filter + date sort only).

### 4.5 Sort icon state indicator

The sort button's glyph reflects the active sort, and is accent-colored whenever the sort is non-default:

| Active sort | Glyph |
|---|---|
| Date · Newest first (default) | ArrowUpDown (grey/neutral) |
| Date · Oldest first | ArrowUp (accent) |
| Score · Highest first | Gauge + down-arrow badge (accent) |
| Score · Lowest first | Gauge + up-arrow badge (accent) |

### 4.6 Config dialogs — minimum score field

- **Feed dialog** (`FeedDialog.vue`): a "Minimum score" number input (min 0, default 0 = keep all) below the Auto-extract toggle, inside a lightly highlighted group. Helper: "Items with score below N are not inserted. Scores refresh and recheck on every fetch."
- **Digest dialog** (`DigestDialog.vue`): a "Minimum score" number input (min 0, default 0 = include all) above the Prompt field. Helper: "Items with score below N are excluded from the digest. Items without score data (plain RSS) are always included."
- **Disabled states (greyed, not hidden):**
  - Feed dialog with a non-score feed type (RSS/Atom): field disabled + greyed, hint "Scores not available for RSS/Atom feeds. Switch to Hacker News, Lobste.rs, or Reddit to enable it."
  - Digest dialog whose selected sources contain no score-capable feed: field disabled + greyed, hint "Scores not available for the selected sources. Add a Hacker News, Lobste.rs, or Reddit source to enable this field."

### 4.7 Defaults

- Default sort: **Newest first (date desc)** — unchanged.
- Default filter: **All** statuses, no score threshold.
- Both icons return to neutral (grey, no dot) when their state equals the default.

## 5. Ergonomics rationale (why Variant A)

| Dimension | A — Slim toolbar (approved) | B — Pane header (declined) | C — State chips (declined) |
|---|---|---|---|
| Footprint / density | Best (reuses existing row) | Medium (new 40 px header) | Worst (widest controls) |
| State visibility | Good (glyphs + dot encode state) | Good (icon dot) | Best (text always visible) |
| Narrow-pane behavior | Excellent (2 icons always fit) | Good | Poor (chips truncate) |
| Consistency with app | High (matches current density; reuses existing status glyphs) | Medium (new header pattern) | Medium (chips are new) |
| Implementation effort | Low (DropdownMenu exists; disabled styling already at 50% opacity) | Low–medium | Medium |

Variant A delivers the required behavior with the smallest change to the current layout, fits every pane width the app already supports, and reuses the existing `DropdownMenu` primitive. The icon-encoded state (status glyph, score dot, sort glyph) keeps the bar at two controls while preserving discoverability through tooltips and ARIA labels.

## 6. Deliverables

- This plan: `docs/ux/score-filter-sort.md`
- Interactive mockup (approved design): `sketches/score-filter-sort/variant-a-toolbar/index.html`
- Screenshots (attached to ticket t_ed1a32ec):
  - `variant-a-supported.png` — HN feed, filter dropdown open (numeric input 50 + Clear), enabled config fields, digest sort open
  - `variant-a-unsupported.png` — RSS feed, sort dropdown open (score options greyed), disabled config fields, digest filter open
  - `variant-a-supported-sort.png` — HN feed, sort dropdown open (date + score, explicit asc/desc), score-desc sort glyph
  - `variant-a-unsupported-filter.png` — RSS feed, filter dropdown open (disabled numeric input + note), disabled config fields
- Historical (declined) alternatives: `sketches/score-filter-sort/{variant-b-pane-header,variant-c-state-chips}/`

## 7. Implementation notes for the child tasks

- **Server-side support is a prerequisite for the feed item view:** items are paginated (50/page), so status/score filtering and date/score sorting must be query params on `GET /feeds/:id/items` (e.g. `min_score`, `sort=date_desc|date_asc|score_desc|score_asc`), mirrored in `sql/queries/items.sql` and `web/src/services/api.ts`. Currently only `status`, `limit`, `offset` exist. The digest results list can sort client-side (small pages) or via a param — decide in t_0c5a1134.
- **`supportsScore` derivation:** from `feed.type` (hn/lobsters/reddit); expose as a computed in the feed store. Saved view: always unsupported.
- **Component replacement:** `FilterBar.vue` is replaced by a two-icon control (new `FilterSortBar` or two components). `ui/DropdownMenu.vue` already provides ARIA keyboard navigation and `disabled` styling — reuse it; add `aria-label`s on the icon buttons and tooltips (`ui/Tooltip.vue` exists) explaining the glyph state.
- **Config fields:** add `min_score` to the feed/digest config payloads (backend tasks t_90b8be54 / t_d5441e8a cover schema + API; the dialog field is part of those tasks' UI scope).
- **Threshold semantics to implement:** `min_score = 0` (or empty) means "no threshold". Items without score data always pass. Re-evaluate scores on every fetch/generation.
- **Sort icon glyph mapping** must live next to the sort state (single source of truth): default → ArrowUpDown, date asc → ArrowUp, score desc/asc → Gauge + down/up badge.

## 8. Confirmation checklist (this ticket re-blocks on these)

The design above is what the regenerated mockups show. Final confirmation requested before implementation tasks (t_0c5a1134, t_2f5e89a6, t_6cdeb0e3, t_90b8be54, t_d5441e8a) start:

1. Approve the revised Variant A as rendered (icons-only bar, status glyph, score dot, numeric threshold with Clear, sort glyphs, greyed-out states everywhere).
2. Default sort "Newest first" (date desc) — confirmed.
3. Anything else to adjust — say so now; after confirmation this ticket closes and implementation proceeds.
