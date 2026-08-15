# Variant 2 — Unified menu

## Design stance
Everything lives behind **one button**: "Unread · ≥50 · Newest first". One dropdown contains Status, Minimum score, and Sort sections separated by dividers.

## Key choices
- Layout: single control in the bar — smallest footprint of all five variants.
- Interaction: 1 click opens the one menu, 1 click changes anything (2 clicks max for any change).
- State visibility: full state text in the button label (status · score · sort).
- Unsupported: score input + score sort greyed inside the same menu (RSS / Saved); digest view hides the score section (status + date only).
- Score: numeric min-score input with Clear/Any; sort = date asc/desc + score asc/desc with explicit labels.

## Trade-offs
- Strong at: absolute minimum footprint; one mental model ("open the menu, pick what you want"); no icon-decoding.
- Weak at: menu is taller (3 sections); changing only the sort requires scanning past status/score; the long label must truncate on narrow panes.

## Best for
Minimalist panes and users who rarely switch sort order — filter is the primary action, sort is secondary.

## Screenshots
- `variant-2-unified-menu-supported.png` — HN, the single menu open (status + min-score 50 + sort, all enabled)
- `variant-2-unified-menu-unsupported.png` — RSS, same menu with score input + score sort greyed
- `variant-2-unified-menu-digest.png` — digest view (no score section at all)
- `variant-2-unified-menu-feed-dialog.png` / `-unsupported.png` — min-score field enabled / greyed
- `variant-2-unified-menu-digest-dialog.png` / `-unsupported.png` — same for digest config

Open `index.html` for the interactive version.
