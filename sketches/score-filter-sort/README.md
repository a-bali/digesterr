# Score filter/sort — UX mockups

Interactive HTML mockups for the score-based filtering/sorting feature (kanban t_ed1a32ec).

## Status

**Variant A (Slim toolbar) was approved by the user on 2026-08-15 with 8 revisions** and
regenerated to match. Variants B and C are kept as historical alternatives (declined).

## Variant A — revised (approved design)

`variant-a-toolbar/index.html` — self-contained page showing the four required screens in a 2x2 grid:

1. Feed config editor with a minimum-score field (enabled for score-capable types, disabled + greyed for RSS)
2. Digest config editor with a minimum-score field (same enabled/disabled treatment)
3. Feed item view: filter + sort icons only, with status glyph, score dot, numeric threshold input, sort glyphs
4. Digest view: status filter + date sort only (no score section)

### Revisions baked in (user, 2026-08-15)

1. Filter bar = **only the two icons** (filter + sort) — no feed tabs, no state label, no reset element
2. Filter icon shows the active **status glyph** after it (layers / eye / check / star / bookmark, matching existing status icons)
3. Active **score filtering** = small dot + accent color on the filter icon
4. Score filtering = **numeric min-score input** (custom threshold, not preset tiers) with a Clear / Any affordance
5. Sort dropdown = **date asc + desc, score asc + desc** (explicit directions)
6. Sort icon indicates the sort in use (arrow direction / gauge+arrow glyph, accent when non-default)
7. Unavailable options **greyed out** — dropdowns and config dialogs (disabled min-score field with hint for non-score feed types; greyed score options for feeds without scores and in the Saved view)
8. Default sort stays **Newest first** (date desc)

### Interactive state controls (mockup chrome, not part of the design)

- Feed config panel: "Lobste.rs (scores)" ↔ "RSS (no scores)"
- Digest config panel: "Score sources" ↔ "RSS-only sources"
- Feed item panel: context "HN (scores)" / "RSS (no scores)" / "Saved view"; menu "closed / filter open / sort open"; sort "Newest ↓ / Oldest ↑ / Score ↓ / Score ↑"
- Digest panel: "closed / filter open / sort open"

### Screenshots

- `variant-a-supported.png` — HN, filter dropdown open (numeric input 50 + Clear), enabled config fields, digest sort open
- `variant-a-unsupported.png` — RSS, sort dropdown open (score options greyed), disabled config fields, digest filter open
- `variant-a-supported-sort.png` — HN, sort dropdown open (all enabled, explicit asc/desc), score-desc sort glyph
- `variant-a-unsupported-filter.png` — RSS, filter dropdown open (disabled numeric input + note), disabled config fields

Serve with `python3 -m http.server` and open in a browser (file:// works too in a normal browser).

See `docs/ux/score-filter-sort.md` for the full UX plan and implementation notes.
