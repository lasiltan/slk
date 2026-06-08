# Plan: "Unread" sidebar section

Date: 2026-06-07

## Behavior

- New section labeled "Unread", rendered **between user-defined custom sections and the default fallback sections** (Channels / DMs / Apps).
- Contains channels that **(a)** are visibly unread (`item.IsVisiblyUnread(state)`) AND **(b)** do NOT carry an explicit `item.Section` (i.e. aren't in a custom user-defined section).
- Channels render in **both** Unread and their default fallback section (duplicated).
- Hidden entirely when no eligible channels exist.
- Config-glob mode only — Slack-native mode (`use_slack_sections=true`) is out of scope; new logic must only fire on the legacy path.

## Affected file

Only `internal/ui/sidebar/model.go`. Tests in `internal/ui/sidebar/*_test.go` get new coverage.

## Design constraint: duplication

Current model assumes one filter index → one section → one nav row. Duplicating channels in Unread breaks that 1:1 unless we represent the duplicate as a separate `navItem` and `renderRow` pointing back to the same filter index. Key consequence: `cursorKey` for `navChannel` becomes `{id, section}` instead of just `{id}`, so cursor stays on the right copy across rebuilds.

## Concrete changes in `model.go`

1. **Constant** — add `defaultUnreadSection = "Unread"` next to the existing default-section constants (model.go:21).

2. **`orderedSectionsLegacy`** (model.go:104) — track `hasUnread bool` while walking filtered items: set when item has `Section == ""` AND `IsVisiblyUnread`. Inject "Unread" into output slice after the `customs` loop and before DMs/Apps/Channels appends. Needs `readStateReader` — either thread the reader through or split into a `Model` method (`m.modelOrderedSections` is the natural place; the legacy free function stays as a back-compat shim that assumes no read state and never emits Unread).

3. **`modelOrderedSections`** (model.go:376) — in the non-Slack branch, do the Unread-aware computation rather than delegating to `orderedSectionsLegacy`. Slack-native branch unchanged.

4. **`navItem`** (model.go:179) — add `section string` field. For `navChannel`, this disambiguates duplicate rows so cursor tracking works.

5. **`rebuildNav`** (model.go:939) — when bucketing filtered items, also emit each unread `Section == ""` item under the "Unread" bucket key. When flattening into nav, set `section` on each `navChannel` to the section it's currently being emitted under. Will need read state — pull from `m.readStateReader` at the top of the function (same nil-safe pattern as `aggregateUnreadForSection`).

6. **`cursorKey` + `currentCursorKey`** (model.go:910) — add `section` to the struct for `navChannel`. `rebuildNavPreserveCursor` (model.go:970) prefers an exact `{id, section}` match; falls back to any `{id}` match so a channel just marked read (and thus losing its Unread copy) keeps the cursor on its remaining default-section row.

7. **`buildCache`** (model.go:1057) — three touch points:
   - `channelNavIdx` (model.go:1093) currently maps `fi → navIdx`. Must become `{fi, section} → navIdx` so the right `navIdx` is stamped on each rendered row.
   - The per-section build loop (model.go:1195) currently iterates `m.filtered` once. Add a parallel pass that, for items qualifying for Unread, also appends a row to `sectionMap[defaultUnreadSection].rows`. Same `channelID`, same active/selected variants — reuse the rendered strings, only `navIdx` differs.
   - `sectionMap` (model.go:1190) needs the "Unread" key when in the ordered sections list.

8. **`aggregateUnreadForSection`** (model.go:1007) — special-case `defaultUnreadSection`: count is the number of items with `Section == ""` AND `IsVisiblyUnread`. Otherwise unchanged.

9. **`sectionFor`** — no change. The Unread duplicate is purely a render/nav-time concern; `sectionFor(item)` still returns the item's "real" home section.

10. **`SelectByID`** (model.go:790) — works as-is: picks the first nav row with matching ID. If channel exists in both Unread and a default section, user lands on whichever appears first (Unread, by render order). Acceptable.

11. **`ClickAt`** (model.go:1537) — works as-is; the duplicate row resolves to the same `ChannelItem`.

## Edge cases

- **Unread section empty after mark-as-read**: `modelOrderedSections` drops "Unread" → rebuild drops its header → cursor on Unread copy falls back via `currentCursorKey` to the default-section copy (the `{id}` fallback above).
- **Collapse state**: "Unread" is a new section name in the `collapsed` map; default behavior (absent key → expanded) is correct. Don't pre-seed it as collapsed in `New` (model.go:491).
- **Filter typing**: name-filtered items already skipped by `rebuildFilter`; Unread iterates same filtered set, so a filtered-out unread channel won't appear in Unread either.
- **Staleness**: same — filter runs first, Unread iterates filtered.
- **Muted unread**: `IsVisiblyUnread` excludes muted, so muted-with-unread channels correctly don't appear in Unread.

## Tests to add

`internal/ui/sidebar/` already has table-driven tests (`model_test.go`, `staleness_test.go`, etc.). New cases:

- Unread section appears after customs, before defaults, when an uncategorized channel is unread.
- Unread section absent when no eligible unreads.
- Channel in custom section that's unread does NOT appear in Unread.
- Channel appears in both Unread and its default Channels/DMs/Apps section simultaneously.
- Marking the only Unread channel read removes the section entirely; cursor preserved on the default-section copy.
- Cursor position survives a collapse toggle of Unread.
- Aggregate badge on collapsed Unread header matches the channel count.

## Out of scope

- Slack-native sections mode — no Unread injection there.
- New config flag — section is always on.
- Sorting within Unread — keep input order to match existing config-mode behavior (no `ChannelOrder` semantics for the duplicate).

## Open decisions (resolved)

- Position: after custom user-defined sections, before defaults.
- Duplication: channels appear in BOTH Unread and their default section.
- Mode: config-glob only.
- Toggle: always on; hidden when empty.
