# Score filter/sort — UX mockups (5 alternatives)

Interactive HTML mockups for score-based filtering/sorting (kanban t_ed1a32ec).

## Status

**Five fresh design alternatives (2026-08-15), replacing the previously rejected
Variant A + B + C.** Prior mockups were discarded per user instruction. No
production code was changed; the user picks a direction before implementation
starts (children t_0c5a1134, t_2f5e89a6, t_6cdeb0e3, t_90b8be54, t_d5441e8a).

## The five alternatives

| # | Dir | Control pattern | Footprint | State visibility | Clicks to change |
|---|---|---|---|---|---|
| 1 | `variant-1-state-labels` | Two labeled buttons whose text IS the state ("Filter: Unread · ≥50" / "Sort: Newest first") + dropdowns | Small | Text — excellent | 2 |
| 2 | `variant-2-unified-menu` | One button ("Unread · ≥50 · Newest first") → single menu with Status + Min-score + Sort | Smallest | Text — excellent | 2 |
| 3 | `variant-3-native-selects` | Native `<select>` status + number input + native `<select>` sort | Medium | Literal — best | 2 (or typed) |
| 4 | `variant-4-direct-manipulation` | Status pills + inline min-score + four sort toggle icons, all always visible | Largest | Absolute — best | 0–1 |
| 5 | `variant-5-pane-header-popover` | 32 px pane header (feed name + count) with two icon popovers (funnel, sort) | Smallest steady-state | Icon + dot — good | 2 |

**Recommended: Variant 1 (state-label buttons)** — clearest active state (text,
no icon decoding), smallest change from the current FilterBar (keeps its slot
and density), two-click changes for everything, and it reuses the existing
`ui/DropdownMenu.vue`. Runner-up: Variant 5 if the feed name in the pane header
is wanted; Variant 4 if zero-click state reading outweighs footprint.

## Shared behavior across all five

- Score = item `points` (hn / lobsters / reddit only). RSS feeds have no scores.
- Saved view mixes feed types → score options greyed (never hidden).
- Digest view: status filter + date sort ONLY — no score section at all.
- Unsupported contexts grey out score options in dropdowns AND disable the
  min-score field in the feed/digest config dialogs, with an explanatory hint.
- Min-score semantics: `0`/empty = any; items without score data always pass.
- Default sort: Newest first (date desc).

## Interactive mockups

Each `index.html` is self-contained. Use the chrome bar to switch scenes
(HN feed / RSS feed / Saved / Digest / Feed dialog / Digest dialog), set
status/score/sort, open the menus, and switch the dialog source type. A
`?scene=&status=&score=&sort=&open=&dlg=` query string also captures any state
directly (used for the screenshots).

Serve with `python3 -m http.server` and open in a browser (file:// works too).

## Screenshots

7 per variant (35 total), one per dir:
`-supported.png` (HN, filter menu open / controls active) ·
`-unsupported.png` (RSS, score greyed) · `-digest.png` (status+date only) ·
`-feed-dialog.png` / `-feed-dialog-unsupported.png` ·
`-digest-dialog.png` / `-digest-dialog-unsupported.png`.

See `docs/ux/score-filter-sort.md` for the full plan, comparison, and the open
decisions the user must answer.
