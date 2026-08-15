## Variant B — Pane header

### Design stance
Give the list pane a real header so filter/sort controls sit in context next to the feed identity.

### Key choices
- Layout: 32 px header row — feed name + unread-count chip left, filter/sort icon buttons right.
- Interaction: same dropdowns as Variant A, anchored under the right-side icons.
- Config fields: min-score field grouped under a "Filtering" section header with a divider.

### Trade-offs
- Strong at: best context (feed name visible in the pane), most discoverable placement, room to grow more controls.
- Weak at: consumes vertical space in every pane, adds chrome to a currently chrome-free list, header needs an empty state (Saved view).

### Best for
Users who want stronger orientation in the list pane and don't mind the extra header row.
