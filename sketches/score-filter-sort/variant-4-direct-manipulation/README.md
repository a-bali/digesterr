# Variant 4 — Direct manipulation

## Design stance
No dropdowns anywhere: **status pills, an inline min-score input, and four one-click sort toggles** are always visible in the bar. State is never hidden and most changes take one click.

## Key choices
- Layout: a segmented pill group (All/Unread/Read/Digested/Saved), a compact "Min score ≥" input + Clear, and four icon sort buttons (date ↓, date ↑, score ↓, score ↑).
- Interaction: 1 click per status; 1 click per sort direction; score typed directly. Zero clicks to read state.
- State visibility: absolute — everything is on screen; active pill and active sort button highlighted with accent.
- Unsupported: score input + score sort buttons disabled + greyed (RSS / Saved); digest view drops the score controls and the Digested/Saved pills.
- Footprint: the widest bar of the five — pills + input + 4 buttons; needs the pane to be ≥ ~420 px or the controls wrap.

## Trade-offs
- Strong at: fewest clicks for status changes (the most common action); zero ambiguity about active state; no menu interactions at all.
- Weak at: largest footprint; four sort icons need tooltips/titles to be understood; icon-only sort buttons are harder to parse than text.

## Best for
Power users and wide desktop panes; anyone who filters/sorts constantly and wants zero-friction access.

## Screenshots
- `variant-4-direct-manipulation-supported.png` — HN: Unread pill active, min-score 50 + Clear, newest-first toggle active
- `variant-4-direct-manipulation-unsupported.png` — RSS: score input + score sort buttons disabled
- `variant-4-direct-manipulation-digest.png` — digest view (status + date only)
- `variant-4-direct-manipulation-feed-dialog.png` / `-unsupported.png` — min-score field enabled / greyed
- `variant-4-direct-manipulation-digest-dialog.png` / `-unsupported.png` — same for digest config

Open `index.html` for the interactive version.
