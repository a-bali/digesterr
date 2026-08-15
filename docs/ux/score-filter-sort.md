# UX Plan — Score-based Filtering & Sorting

Task: t_ed1a32ec (parent of the implementation tasks: feed config min score, digest config min score, feed item view filter/sort UI, digest view filter/sort UI).

Status: **awaiting user approval** — no production code changed.

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

## 4. Design variants

Three alternatives were mocked up (attached as screenshots; interactive HTML in `sketches/score-filter-sort/`). All three keep the two-icon requirement and differ in placement/density.

### Variant A — "Slim toolbar" ⭐ recommended

- The existing filter row keeps its footprint (`p-2 border-b`): two ghost icon buttons (**Filter**, **Sort**), plus a compact state label ("Unread · Newest") and a reset affordance when a non-default combination is active.
- Icons turn accent-colored and show a small dot when their menu differs from the default.
- **Filter dropdown** (reka-ui `DropdownMenu`, anchored right):
  - Status section: All / Unread / Read / Digested / Saved (check on active).
  - Divider.
  - Score section: Any score / ≥ 10 / ≥ 50 / ≥ 100 / ≥ 250. When unsupported: items disabled + greyed (50% opacity), caption "Scores not available for this feed".
- **Sort dropdown**:
  - Date: Newest first (default) / Oldest first.
  - Divider.
  - Score: Highest first / Lowest first (disabled + greyed when unsupported).
- **Config dialogs:** plain number input (min 0, default 0 = keep all) with one-line helper text. Feed dialog: below the Auto-extract toggle. Digest dialog: its own row above the prompt.
- Pros: zero layout change, smallest footprint, works at today's narrow pane widths (no collapse logic needed — icons never overflow), dropdown pattern already in the app (`ui/DropdownMenu.vue` supports disabled items), state label gives feedback without extra chrome.
- Cons: icon-only controls are less self-explanatory to first-time users; the state label carries the discoverability burden.

### Variant B — "Pane header"

- The list pane grows a proper header row (`h-10`): **feed name + unread count on the left**, filter + sort icons on the right, dropdowns anchored under the icons.
- Same dropdown contents as Variant A.
- **Config dialogs:** the min-score field lives under a "Filtering" section header with a divider, visually grouping threshold settings.
- Pros: strongest context (feed name visible in the pane, currently only in the sidebar), most discoverable placement, room to grow more controls.
- Cons: consumes ~40 px of vertical space in every pane, adds chrome to a currently chrome-free list, header needs to adapt when no feed is selected (Saved view).

### Variant C — "State chips"

- Toolbar row with two labeled chips: **"Filter: Unread ▾"** and **"Sort: Newest ▾"** — the current value is always visible; clicking a chip opens its dropdown. A small "Reset" text button appears when non-default.
- **Config dialogs:** stepper number input (− / + buttons).
- Pros: most explicit; state is readable at a glance without opening anything.
- Cons: widest controls — truncates or wraps on narrow panes (the very problem the current `FilterBar` collapse logic exists for); three controls (2 chips + reset) compete for attention; more code than the alternatives.

## 5. Ergonomics comparison

| Dimension | A — Slim toolbar | B — Pane header | C — State chips |
|---|---|---|---|
| Footprint / density | Best (reuses existing row) | Medium (new 40 px header) | Worst (widest controls) |
| State visibility | Good (icon dot + label) | Good (icon dot) | Best (text always visible) |
| Discoverability | Medium (label helps) | High (header context) | High |
| Narrow-pane behavior | Excellent (2 icons always fit) | Good | Poor (chips truncate) |
| Consistency with app | High (matches current density) | Medium (new header pattern) | Medium (chips are new) |
| Implementation effort | Low (DropdownMenu exists) | Low–medium | Medium |

**Recommendation: Variant A.** It delivers the required behavior with the smallest change to the current layout, fits every pane width the app already supports, and reuses the existing `DropdownMenu` primitive (which already renders disabled items at 50% opacity — exactly the greyed-out state requested). Variant B is a strong runner-up if the user wants the feed name visible in the pane; Variant C is the most explicit but fights the app's density.

## 6. Score filter presets (default proposal)

Fixed tiers keep a dropdown ergonomic: **Any score / ≥ 10 / ≥ 50 / ≥ 100 / ≥ 250** (HN-scale). Alternative: a custom minimum-score input inside the dropdown (more flexible, heavier). Open question for the user (Q2 below).

## 7. Open decisions — user must choose before implementation

1. **Which variant?** A (recommended) / B / C / hybrid.
2. **Score filter style:** fixed tiers (recommended) or custom threshold input in the dropdown.
3. **Saved items view:** grey out score options (recommended, consistent) or hide them entirely.
4. **Default sort:** keep "Newest first" (date desc) as the default — confirm.
5. **RSS feed dialog:** show the min-score field disabled with a hint (recommended) or hide it for non-score feed types.
6. **Digest min-score label/placement** as mocked — confirm.

## 8. Implementation notes for the child tasks

- **Server-side support is a prerequisite for the feed item view:** items are paginated (50/page), so status/score filtering and date/score sorting must be query params on `GET /feeds/:id/items` (e.g. `min_score`, `sort=date_desc|date_asc|score_desc|score_asc`), mirrored in `sql/queries/items.sql` and `web/src/services/api.ts`. Currently only `status`, `limit`, `offset` exist. The digest results list can sort client-side (small pages) or via a param — decide in t_0c5a1134.
- **`supportsScore` derivation:** from `feed.type` (hn/lobsters/reddit); expose as a computed in the feed store. Saved view: always unsupported.
- **Component replacement:** `FilterBar.vue` is replaced by a two-icon control (new `FilterSortBar` or two components). `ui/DropdownMenu.vue` already provides ARIA keyboard navigation and `disabled` styling — reuse it, add `aria-label`s on the icon buttons.
- **Config fields:** add `min_score` to the feed/digest config payloads (backend tasks t_90b8be54 / t_d5441e8a cover schema + API; the dialog field is part of those tasks' UI scope).
- **Threshold semantics to implement:** `min_score = 0` means "no threshold". Items without score data always pass. Re-evaluate scores on every fetch/generation.
