# Variant 1 — State-label buttons

## Design stance
Keep the current FilterBar slot; replace the five segmented buttons with **two labeled buttons whose text IS the current state** ("Filter: Unread · ≥50", "Sort: Newest first"). Dropdown menus open under each.

## Key choices
- Layout: unchanged bar footprint (`p-2 border-b`); two `height-26px` ghost buttons.
- State visibility: the button label reads out exactly what's applied — no icon decoding needed.
- Interaction: 1 click opens menu, 1 click picks an option (2 clicks to change).
- Score: numeric min-score input inside the filter menu with Clear/Any; date + score sort (explicit asc/desc) inside the sort menu.
- Unsupported: score section greyed in menus (RSS feed / Saved view); digest view hides the score section entirely.

## Trade-offs
- Strong at: clearest textual state; zero learning curve; smallest behavior change from today's FilterBar.
- Weak at: widest controls (labels are long); two menus instead of one; on very narrow panes the labels must truncate.

## Best for
Users who want to always see what filter/sort is active at a glance, and a minimal delta from the current UI.

## Screenshots
- `variant-1-state-labels-supported.png` — HN, filter menu open (status + min-score 50 + Clear)
- `variant-1-state-labels-unsupported.png` — RSS, sort menu open (score options greyed)
- `variant-1-state-labels-digest.png` — digest view (status + date only, no score section)
- `variant-1-state-labels-feed-dialog.png` / `-unsupported.png` — min-score field enabled / greyed
- `variant-1-state-labels-digest-dialog.png` / `-unsupported.png` — same for digest config

Open `index.html` in a browser for the interactive version (scene tabs, open filter/sort buttons, dialog type switch).
