# Variant 3 — Native selects

## Design stance
No custom UI at all: the bar is a **status `<select>`, a min-score number input, and a sort `<select>`** — plain form controls with native dropdowns.

## Key choices
- Layout: three controls side by side; slightly wider bar than variants 1/2.
- Interaction: 2 clicks for a dropdown option (open native menu, pick); score typed directly (no menu at all).
- State visibility: best-in-class — the current status and sort are always literally visible in the control itself.
- Unsupported: score input disabled + greyed, score options disabled in the sort select (RSS / Saved); digest view drops the score input and score options.
- Score: numeric input with Clear/Any; sort select lists all four date/score directions.
- Implementation: trivially cheap — reuses `<select>` and a number input; no menu component needed.

## Trade-offs
- Strong at: maximum clarity (state is never hidden), zero custom dropdown code, keyboard-friendly (native selects are fully accessible).
- Weak at: least polished look — native selects don't match the app's custom menu styling; three controls take more horizontal room; two separate dropdowns to remember.

## Best for
Teams that value simplicity and accessibility over aesthetics, and where implementation effort matters most.

## Screenshots
- `variant-3-native-selects-supported.png` — HN: status select (Unread), min-score 50 + Clear, sort select (Newest first)
- `variant-3-native-selects-unsupported.png` — RSS: score input disabled, note shown
- `variant-3-native-selects-digest.png` — digest view (status + date only)
- `variant-3-native-selects-feed-dialog.png` / `-unsupported.png` — min-score field enabled / greyed
- `variant-3-native-selects-digest-dialog.png` / `-unsupported.png` — same for digest config

Open `index.html` for the interactive version.
