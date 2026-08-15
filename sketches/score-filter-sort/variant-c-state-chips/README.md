## Variant C — State chips

### Design stance
Make the current filter/sort values permanently visible as labeled chips — no guessing, no opening menus to check.

### Key choices
- Layout: two chips ("Filter: Unread ▾", "Sort: Newest ▾") in the toolbar row; reset link appears when non-default.
- Interaction: chips open the same dropdowns; chip border/accent reflects active state.
- Config fields: stepper number input (− / + buttons).

### Trade-offs
- Strong at: most explicit; state readable at a glance.
- Weak at: widest controls (truncates on narrow panes — the problem the current FilterBar collapse logic exists to solve); busier row; more code than A or B.

### Best for
Users who prioritize glanceable state over density, on wide screens.
