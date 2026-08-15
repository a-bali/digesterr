# Score filter/sort — UX mockups

Interactive HTML mockups for the score-based filtering/sorting feature (kanban t_ed1a32ec).
Each variant is a self-contained page showing the four required screens in a 2x2 grid:

1. Feed config editor with a minimum-score field
2. Digest config editor with a minimum-score field
3. Feed item view with filter + sort icons (status/score filtering, date/score sorting; score options greyed when unsupported)
4. Digest view with filter/sort limited to status filtering and date sorting

Each page has interactive tabs:
- Feed item panel: "HN (scores)" ↔ "RSS (no scores)" toggles the greyed-out score options.
- Digest panel: "Filter open" ↔ "Sort open" toggles the two dropdowns.

Screenshots (PNG, per variant): `*-supported.png` (HN state, filter open) and `*-unsupported.png` (RSS greyed state, sort open).

Serve with `python3 -m http.server` and open in a browser (file:// works too in a normal browser).

See `docs/ux/score-filter-sort.md` for the full UX plan, comparison, and recommendation.
