# UX Plan — Score-based Filtering & Sorting (5 alternatives)

Task: t_ed1a32ec (parent of the implementation tasks: feed config min score,
digest config min score, feed item view filter/sort UI, digest view filter/sort UI).

Status: **draft v3 — five fresh alternatives rendered, awaiting user choice.
No production code changed.**

> History: v1 (3 variants, Variant A recommended) was approved with 8 revisions,
> then the whole approach was rejected on 2026-08-15 with instruction to start
> fresh and produce at least 5 different alternatives. Variants A/B/C are
> discarded; only this document and `sketches/score-filter-sort/` reflect the
> current direction.

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

- **Score = the item's `points` value** — HN score, Lobste.rs points, Reddit
  upvotes. It is already displayed in the item list with the ↑ icon (`ItemList.vue`).
- **Score-capable feed types:** `hn`, `lobsters`, `reddit`. Plain RSS/Atom feeds
  have no score data (points stay 0).
- **Detection rule (client):** a feed supports scores iff `feed.type ∈ {hn, lobsters, reddit}`.
  The **Saved items** view mixes feeds of both kinds, so score filter/sort is
  unavailable there (options greyed out, same treatment as an unsupported feed).
- **Threshold rule (shared by feed ingestion and digest generation):** the
  configured minimum score applies **only to items that have score data**.
  Items without a score (RSS items) are never blocked by the threshold.
- **Refresh rule:** scores rise over time (an HN story gains points), so
  thresholds are re-evaluated on every fetch (feed ingestion) and at generation
  time (digests), using freshly enriched scores.

## 3. Required behavior (from the ticket)

1. Feed config: minimum score — items below it are not inserted; rechecked every fetch.
2. Digest config: minimum score — items below it are excluded from the digest.
3. Feed item view: filter + sort controls (status + score filtering; date +
   score sorting); **score options greyed out when the feed doesn't support scores**.
4. Digest view: **status filtering + date sorting only — no score options at all**.
5. Unavailable options are greyed (not hidden) in dropdowns AND config dialog fields.
6. No production code changes in this planning task.

## 4. Five design alternatives

All five cover the same four surfaces and obey the same grey-out rules. They
differ in control pattern, footprint, and state visibility. Each has an
interactive `index.html` + 7 screenshots in `sketches/score-filter-sort/`.

| # | Name | Pattern | Footprint | State visibility | Clicks | Effort |
|---|---|---|---|---|---|---|
| 1 | State-label buttons | Two labeled buttons: "Filter: Unread · ≥50", "Sort: Newest first"; dropdowns under each | Small (reuses current bar) | Text — excellent | 2 | Low |
| 2 | Unified menu | One button "Unread · ≥50 · Newest first" → single menu: Status + Min-score + Sort | Smallest | Text — excellent | 2 | Low |
| 3 | Native selects | `<select>` status + number input + `<select>` sort | Medium | Literal — best | 2 / typed | Lowest |
| 4 | Direct manipulation | Status pills + inline min-score + 4 sort toggle icons, always visible | Largest | Absolute — best | 0–1 | Medium |
| 5 | Pane header popovers | 32 px header (feed name + count) + funnel/sort icon popovers | Smallest steady-state | Icon + dot — good | 2 | Medium |

### 4.1 Variant 1 — State-label buttons

- The current FilterBar slot keeps its footprint. It holds **two ghost buttons**
  whose label text IS the applied state: `Filter: Unread · ≥50` /
  `Sort: Newest first`.
- Filter menu: status items (All/Unread/Read/Digested/Saved, check on active) +
  divider + **numeric min-score input** (0/empty = any) with **Clear/Any** and
  helper "Items below N points are hidden. 0 = any score."
- Sort menu: **Date: Newest first (default) / Oldest first**; divider; **Score:
  Highest first / Lowest first** — explicit asc/desc for both dimensions.
- Unsupported (RSS feed, Saved view): score input disabled + greyed, score sort
  items disabled + greyed, note "Scores not available for this feed." / "…for
  saved items (mixed feeds)." — greyed, never hidden.
- Digest view: menu shows status + date sort only; the score section is not present.

### 4.2 Variant 2 — Unified menu

- One button whose label composes the full state: `Unread · ≥50 · Newest first`.
- One dropdown with three sections: **Status**, **Minimum score**, **Sort**,
  separated by dividers. All interactions happen in that one menu.
- Same grey-out rules and digest-view restriction as Variant 1 (score section
  hidden in digest; score controls greyed for RSS/Saved).

### 4.3 Variant 3 — Native selects

- The bar is plain form controls: status `<select>`, a compact `Min score ≥`
  number input with Clear/Any, and a sort `<select>` listing all four
  date/score directions.
- Native dropdowns = zero custom menu code; keyboard/accessibility free.
- Unsupported: score input disabled + greyed with a note; score options
  disabled in the sort select. Digest view: score input and score options gone.

### 4.4 Variant 4 — Direct manipulation

- Everything visible at all times: status **pill group**, inline `Min score ≥`
  input + Clear, and **four sort toggles** (date ↓ / date ↑ / score ↓ / score ↑)
  with the active one highlighted.
- 0–1 clicks for any change; state can never be hidden.
- Unsupported: score input + score sort toggles disabled + greyed; digest view
  hides score controls and the Digested/Saved pills.
- Widest bar — needs ~420+ px pane width or controls wrap.

### 4.5 Variant 5 — Pane header popovers

- A 32 px pane header: feed name + unread count on the left; two icon buttons
  (funnel, sort glyph) on the right, opening anchored popovers.
- Filter icon shows **accent + small dot** when any filter (status ≠ All or
  min-score set) is active; sort glyph changes shape and accents when
  non-default (Newest first = neutral ⇅).
- Filter popover: status + numeric min-score (Clear/Any). Sort popover: date
  asc/desc + score asc/desc. Grey-out rules as above; digest view hides score.

## 5. Recommendation: Variant 1 — State-label buttons

| Dimension | 1 Labels | 2 Unified | 3 Native | 4 Direct | 5 Header |
|---|---|---|---|---|---|
| Footprint | Small (reuses current bar) | Smallest | Medium | Largest | Smallest steady-state |
| State clarity | Text — excellent | Text — excellent | Literal — best | Absolute — best | Icon + dot — good |
| Change cost | 2 clicks | 2 clicks | 2 clicks / typed | 0–1 clicks | 2 clicks |
| Custom UI code | Low (DropdownMenu) | Low | None | Medium | Medium |
| Narrow-pane fit | Good (labels truncate) | Good (label truncates) | Good | Poor (wraps) | Best (2 icons) |
| Consistency with app | High (matches current density) | High | Low (native styling) | Medium (new pill pattern) | Medium (new header row) |

**Why Variant 1:** it has the clearest active-state visibility without any icon
decoding (the button *says* what's applied — "Filter: Unread · ≥50"), it is the
smallest change from today's FilterBar (same slot, same density, no new header
row), and it reuses the app's existing `ui/DropdownMenu.vue` with its built-in
disabled styling. Variant 2 is nearly as good but buries sort inside a taller
menu; Variant 4 is the best for heavy filtering but costs the most horizontal
space; Variant 5 is the most "app-like" but encodes state in icons.

**The final choice is the user's** — see §8.

## 6. Config dialog fields (all variants)

- **Feed dialog** (`FeedDialog.vue`): a "Minimum score" number input (min 0,
  default 0 = keep all) below the Auto-extract toggle. Helper: "Items with
  score below N are not inserted. Scores refresh and recheck on every fetch."
- **Digest dialog** (`DigestDialog.vue`): a "Minimum score" number input (min 0,
  default 0 = include all) above the Prompt field. Helper: "Items with score
  below N are excluded from the digest. Items without score data (plain RSS)
  are always included."
- **Disabled states (greyed, not hidden):**
  - Feed dialog with a non-score feed type: disabled + hint "Scores not
    available for RSS / Atom feeds. Switch to Hacker News, Lobste.rs, or Reddit
    to enable this field."
  - Digest dialog whose sources contain no score-capable feed: disabled + hint
    "Scores not available for the selected sources. Add a Hacker News,
    Lobste.rs, or Reddit source to enable this field."
- The field itself is identical across all five variants (only the surrounding
  control row differs), so the dialog mockups are shared.

## 7. Deliverables

- This plan: `docs/ux/score-filter-sort.md`
- Interactive mockups: `sketches/score-filter-sort/{variant-1-state-labels,variant-2-unified-menu,variant-3-native-selects,variant-4-direct-manipulation,variant-5-pane-header-popover}/index.html`
- 35 screenshots (7 per variant: supported / unsupported / digest /
  feed-dialog / feed-dialog-unsupported / digest-dialog / digest-dialog-unsupported),
  attached to ticket t_ed1a32ec
- Per-variant READMEs with one-line pros/cons rationale

## 8. Open decisions — please answer to approve

1. **Which variant?** 1 (recommended) / 2 / 3 / 4 / 5 / hybrid.
2. Score filter style: numeric min-score input (all variants use it) — keep, or
   prefer preset tiers / a slider?
3. Saved items view: grey out score options (recommended) or hide them entirely?
4. Default sort stays "Newest first" (date desc) — OK?
5. RSS feed dialog: show the min-score field disabled with a hint (recommended)
   or hide it for non-score feed types?

Reply to the ticket with your choices; implementation tasks
(t_0c5a1134, t_2f5e89a6, t_6cdeb0e3, t_90b8be54, t_d5441e8a) start after this
ticket closes.

## 9. Implementation notes for the child tasks

- **Server-side support is a prerequisite for the feed item view:** items are
  paginated (50/page), so status/score filtering and date/score sorting must be
  query params on `GET /feeds/:id/items` (e.g. `min_score`,
  `sort=date_desc|date_asc|score_desc|score_asc`), mirrored in
  `sql/queries/items.sql` and `web/src/services/api.ts`. Currently only
  `status`, `limit`, `offset` exist. The digest results list can sort
  client-side (small pages) or via a param — decide in t_0c5a1134.
- **`supportsScore` derivation:** from `feed.type` (hn/lobsters/reddit); expose
  as a computed in the feed store. Saved view: always unsupported.
- **Component replacement:** `FilterBar.vue` is replaced by the chosen control.
  `ui/DropdownMenu.vue` already provides ARIA keyboard navigation and
  `disabled` styling — reuse it for menu variants; add tooltips/`aria-label`s
  on icon buttons.
- **Config fields:** add `min_score` to the feed/digest config payloads
  (backend tasks t_90b8be54 / t_d5441e8a cover schema + API; the dialog field
  is part of those tasks' UI scope).
- **Threshold semantics to implement:** `min_score = 0` (or empty) means "no
  threshold". Items without score data always pass. Re-evaluate scores on every
  fetch/generation.
