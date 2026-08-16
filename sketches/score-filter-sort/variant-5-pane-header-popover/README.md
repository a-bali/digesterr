# Variant 5 — Pane header popovers

## Design stance
Give the list pane a real **32 px header**: feed name + unread count on the left, two compact icon buttons (filter funnel, sort glyph) on the right, opening anchored popovers. (This is the classic "list toolbar" pattern from Linear / Slack / Notion.)

## Key choices
- Layout: a new header row replaces the plain filter bar; icons occupy ~52 px total on the right.
- Interaction: 1 click opens a popover, 1 click picks (2 clicks to change); icons indicate active state with accent color + a small dot on the filter icon.
- State visibility: good — accent + dot when non-default, but the exact value (e.g. "≥50") is only visible in the popover; the feed name gains permanent visibility as a bonus.
- Unsupported: score input + score sort greyed in the popovers (RSS / Saved); digest view hides the score section.
- Score: numeric min-score input with Clear/Any in the filter popover; sort popover = date asc/desc + score asc/desc.
- Implementation: medium — needs the header row plus two popovers; reuses the app's existing DropdownMenu primitive.

## Trade-offs
- Strong at: smallest steady-state footprint (two 26 px icons); the pane finally shows which feed you're in; familiar pattern from modern apps.
- Weak at: active state is encoded (icon + dot), not spelled out — a user must open the popover to see "≥50"; new header row is a layout change.

## Best for
Anyone who wants a clean, app-like toolbar and values screen real estate; the runner-up if textual state isn't required.

## Screenshots
- `variant-5-pane-header-popover-supported.png` — HN: header with name + count, filter popover open (status + min-score 50 + Clear), filter icon accent + dot
- `variant-5-pane-header-popover-unsupported.png` — RSS: sort popover open with score options greyed
- `variant-5-pane-header-popover-digest.png` — digest view (no score section)
- `variant-5-pane-header-popover-feed-dialog.png` / `-unsupported.png` — min-score field enabled / greyed
- `variant-5-pane-header-popover-digest-dialog.png` / `-unsupported.png` — same for digest config

Open `index.html` for the interactive version.
