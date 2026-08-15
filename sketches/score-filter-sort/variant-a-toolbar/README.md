## Variant A — Slim toolbar (APPROVED, revised 2026-08-15)

### Design stance
Keep the existing filter-row footprint; replace the segmented buttons with two ghost icon buttons (Filter + Sort). All state is carried by the icons themselves — no label, no reset element.

### Key choices (user-approved revisions)
- **Bar contents:** exactly two icon buttons. No feed tabs, no state label, no reset.
- **Filter icon** = funnel + a small status glyph after it (Layers / CircleDot / CircleCheck / Sparkles / Bookmark, matching the app's status icons). Active **score threshold** = accent color + small dot on the funnel.
- **Filter dropdown:** status section (All / Unread / Read / Digested / Saved) + divider + Score section with a **numeric min-score input** (custom threshold, 0 = any) and a **Clear** affordance. Unsupported contexts disable + grey the input with a note.
- **Sort icon** = active-sort glyph: ArrowUpDown (date · newest, default, grey), ArrowUp (date · oldest), gauge + down/up badge (score · highest / lowest) — accent when non-default.
- **Sort dropdown:** Date (Newest first / Oldest first) + divider + Score (Highest first / Lowest first); score options greyed when unsupported. Digest view has no score section at all.
- **Config fields:** plain number input (min 0, default 0 = keep all) with one-line helper; disabled + greyed with a hint for non-score feed types (feed dialog) / non-score sources (digest dialog).

### Trade-offs
- Strong at: smallest footprint, zero layout change, fits every pane width (no collapse logic needed), lowest implementation effort, reuses `ui/DropdownMenu.vue` disabled styling.
- Weak at: icon-only controls; first-time users need the glyphs + tooltips to learn the state encoding.

### Best for
Users who want the feature with minimal visual disruption to the current UI (this is the approved option).
