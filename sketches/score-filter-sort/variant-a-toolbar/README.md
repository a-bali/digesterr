## Variant A — Slim toolbar (recommended)

### Design stance
Keep the existing filter-row footprint; replace the segmented buttons with two ghost icon buttons and let a compact state label carry the feedback.

### Key choices
- Layout: two 24 px icon buttons (Filter, Sort) in the current `p-2 border-b` row; state label + reset on the right.
- Interaction: reka-ui `DropdownMenu` anchored right; check mark on the active item; icons get an accent dot when non-default.
- Config fields: plain number input (min 0, default 0) with one-line helper, directly under the Auto-extract toggle (feed) and above the prompt (digest).

### Trade-offs
- Strong at: smallest footprint, zero layout change, fits every pane width (no collapse logic needed), lowest implementation effort.
- Weak at: icon-only controls need the state label for discoverability.

### Best for
Users who want the feature with minimal visual disruption to the current UI.
