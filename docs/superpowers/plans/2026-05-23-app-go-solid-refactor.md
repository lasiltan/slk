# internal/ui/app.go SOLID Refactor — Implementation Plan

> **Status:** Phases 0–2 complete (10 cohesive-state extractions landed). Phases 3–7 ahead.
> **Branch:** `refactor/app-phase-2-extract-state-objects` (tip; earlier phases on their own branches off main).
> **Working baseline:** `f2defed` (main as of the rebase, includes upstream wheel-scroll + click-to-thread changes).

**Goal:** Apply SOLID principles to the 6,200-line God Object that is `internal/ui/app.go`. The `App` struct previously held ~95 fields and ~120 methods spanning at least a dozen unrelated concerns (mouse FSM, image preview overlay, navigation history, typing indicators, presence/DND, edit state, ...). Reduce App's surface area, separate concerns into self-contained collaborators, and prepare the file for further structural work (reducer split, mode-handler strategy, View region split).

**Architecture:** Incremental, behavior-preserving extractions. Each phase ships green tests with no observable behavior change. App keeps the orchestration logic that couples sub-models; new controllers own the cohesive state + invariants that previously lived as raw fields on App.

**Tech Stack:** Go 1.26+, bubbletea v2, lipgloss v2. Tests are plain `testing.T` in white-box `package ui` style.

**Reference (in chat, not on disk):** Original 7-phase plan was laid out in the initial brainstorming response on 2026-05-23. This document captures both the original plan and how execution has diverged from it.

---

## Pre-flight

Confirmed before Phase 0:

- `go test ./internal/ui/...` baseline: all 24 packages green
- View benchmarks exist in `app_bench_test.go` (`BenchmarkAppViewCompose`, `BenchmarkAppViewIdle`) — used as perf guard rails

## Running tally vs original baseline

| | Original | After Phase 2 | After Phase 3 | Δ from original |
|---|---|---|---|---|
| `app.go` lines | 6,216 | 5,099 | **4,920** | **−1,296 (−20.8%)** |
| `App` struct fields | ~95 | ~60 | ~40 | ~−55, consolidated into 10 controllers + 4 service interfaces |
| `App` callback `Set*` methods | ~28 | ~28 | **4** | **−24** (24 collapsed into 4 service setters) |
| main.go wiring calls | ~40 | ~40 | ~20 | **−20** |
| Cohesive new files under `internal/ui/` | 0 | 12 | 14 | services.go + services_helpers_test.go |

---

## Phase 0 — Safety net: characterization tests

**Goal:** Pin behavior of every Phase 2 extraction target that lacks adequate test coverage. No production code changes.

**Status:** **COMPLETE** — commit `c5653b1`.

**Survey done:** existing tests already comprehensive for navHistory (13 tests), edit state (~7 tests), typing tracker (6 tests), self-send dedup (covered via integration), image preview (10 tests), selection/drag (covered via simulated mouse events). Gaps existed for `panelCache.hit/store`, `panelAt`, presence (`applyOptimisticStatus`, `StatusChangeMsg`), and the workspace-bootstrap overlay state machine.

**Files added (4, +35 tests):**

- `internal/ui/app_panelcache_test.go` (5 tests: hit/store key semantics, miss conditions, overwrite)
- `internal/ui/app_panelat_test.go` (11 tests: coordinate-band mapping, border-strip, status row, visibility flags)
- `internal/ui/app_presence_test.go` (8 tests: `applyOptimisticStatus` for all 4 actions, `StatusChangeMsg` per-team cache, DND ticker single-claim guard, expired DND no-tick)
- `internal/ui/app_loading_test.go` (11 tests: `SetLoadingWorkspaces` seeding, `MarkWorkspaceReady/Failed`, `checkLoadingDone` thresholds, `renderLoadingOverlay` content)

---

## Phase 1 — Mechanical split: messages and callbacks

**Goal:** Move every `*Msg` type and `*Func` callback type out of `app.go` into dedicated files under the same package. Pure code motion; zero semantic change.

**Status:** **COMPLETE** — commit `da1f53b`.

**Files added:**

- `internal/ui/msgs.go` (485 lines) — all `*Msg` types: exported (`ChannelSelectedMsg`...`ToastMsg`, message-action family) and file-private (`previewLoadedMsg`, `threadFetchDebounceMsg`, `autoScrollTickMsg`, `editEmptyToastMsg`)
- `internal/ui/callbacks.go` (124 lines) — all `*Func` callback types, `ChannelVisitRecorder`, `ChannelLookupFunc`, `clipboardReader` type + `defaultClipboardReader` var

**Diff shape:** 560 deletions / 0 additions in `app.go` (pure removal; types compiled with the same names in the new files). All callers and tests compiled unchanged.

`app.go`: 6,216 → 5,656 (−560).

---

## Phase 2 — Extract cohesive state objects (Information Holders)

**Goal:** Each multi-field stateful concern gets its own type + file. App holds one controller pointer per concern instead of the raw fields. The orchestrators that couple to sub-models stay on App; the new controllers own pure state + invariants and are testable in isolation.

**Status:** **COMPLETE** — 10 extractions over commits `def1ca5..036555a`.

### Phase 2 summary table

| Sub | Controller | Commit | Δ app.go | Notable |
|---|---|---|---|---|
| 2a | `navHistoryStore` | `def1ca5` | −116 | First; established the controller-pattern + test-rewire pattern. Pure data + `Walk` method that returns `(id, name, type, ok)` and lets App wrap into a `tea.Cmd`. |
| 2b | `selfSendDedup` | `b1ee302` | −100 | 5 methods + 3 fields under one owner. Two cooperating dedup windows (in-flight + ts-exact). |
| 2c | `workspaceBootstrap` | `9c7961f` | −79 | First **once-claim guard** (`ClaimInitialActive`). `Render` takes spinner glyph as parameter so no `styles` dependency. |
| 2d | `typing` (`typingTracker` + `typingBroadcaster`) | `1185045` | −89 | Two cohesive types in one file; broadcaster holds `*typingTracker` for shared `Enabled` check. Caught + reverted an unintended `Enabled()` gate on `Add`. |
| 2e | `editController` | `7ca4a80` | −18 | `Matches(channelID, ts)` collapses 3-clause guard at 2 sites. Zero test changes (white-box access still works through pointer). |
| 2f | `presenceController` | `c00033a` | −53 | Second once-claim guard (`ClaimTicker`). **Caught a semantic-preservation bug**: needed 4-return `Status(...)` with `ok` to distinguish "no entry" from "all-zeros entry" in DNDTickMsg arm. |
| 2g | `panelRenderCache` | `4a7747f` | −47 | Pure grouping: 6 `panelCache*` fields → 1 `*panelRenderCache` with 6 named subfields. Zero test changes; type itself was already in `panelcache.go`. |
| 2h | `dragSelection` | `6ee5654` | −26 | Third once-claim guard (`ClaimAutoScroll`). **Pattern: tuple-return finishers** — `Extend(panel,px,py)→(x,y)` owns the clamp invariant; `Finish()→(moved,panel,clickedMessage)` capture-and-reset. |
| 2i | `imagePreviewController` | `5523019` | −22 | Drew tight boundary: only the 4 overlay-state fields moved. Cmd helpers stayed on App (too tightly coupled to messagepane/threadPanel via `findMessageInActiveChannel`). |
| 2j | `panelLayout` | `036555a` | −61 | Highest-risk extraction (View-adjacent). `Compute(...) → panelLayoutFrame` resolver returns explicit `ThreadAutoHidden` flag so the side effect (`a.threadVisible = false` + focus steal) becomes visible at the call site. |

### Patterns that emerged during Phase 2

Worth naming because they'll recur:

1. **Once-claim guard.** Three instances (`ClaimInitialActive`, `ClaimTicker`, `ClaimAutoScroll`) — each returns `true` exactly once until paired `Clear*`. Rule-of-Three tripwire is active; if a fourth appears, extract a tiny `OnceGate` type.

2. **Capture-then-reset → tuple-return.** `dragState.Finish() → (moved, panel, clickedMessage)`, `presence.ClearDNDFor(team) → workspaceStatus`. Combine "read current state" + "reset to zero" into one method that returns the captured tuple. Removes the read-after-reset trap.

3. **`Matches` predicate.** `editing.Matches(ch, ts)`, `preview.Active()`. Collapse a multi-clause guard appearing at multiple call sites into a named query. The grep-ability win matters as much as the line-count win.

4. **Frame-struct return.** `panelLayout.Compute() → panelLayoutFrame`. When a computation has many outputs, return a struct rather than 8 named locals. Documents intent + survives field additions.

5. **App keeps the orchestrator; controller keeps the state.** Every Phase 2 extraction except `panelRenderCache` (which has no behavior). Orchestrators couple to sub-models; controllers are pure data + invariants. This is the boundary heuristic — when in doubt about whether a method belongs on the controller, ask "does it call into a sub-model or dispatch a tea.Cmd?" If yes, it's an orchestrator and stays on App.

### Verification after Phase 2

- `go vet ./...` clean · `go build ./...` clean
- 39/39 packages green, 24/24 in `internal/ui/...`
- View benchmarks healthy (`BenchmarkAppViewCompose ~2.0ms`, `BenchmarkAppViewIdle ~1.7ms` — no regression)
- All Phase 0 characterization tests green
- All upstream-merged tests (click-opens-thread, scroll-decouple, wheel-scroll-config, thread-parent-scrolls) green

### One upstream rebase along the way

Mid-Phase-2 (after 2d, before 2e), `origin/main` advanced by 6 commits (#26 wheel/PageUp scroll decoupling, configurable wheel speed, click-to-open-thread, thread parent scrolling, etc.). Rebased the tip branch onto the new main. Conflicts: just 1 in `app.go` (the App field block where upstream added `mouseWheelLines` near the typing fields that Phase 2d had collapsed). Plus one trailing test reference (`app.loading = false` in an upstream-added test that needed migration to `app.bootstrap.loading = false`). All other 5 commits rebased clean.

---

## Phase 3 — Service interfaces (DIP + ISP)

**Goal:** Replace the flat callback fields on App with cohesive service interfaces. Collapse the per-callback `Set*` methods on App into per-service `Set*` methods. Shrink main.go's wiring surface.

**Status:** **COMPLETE** — 4 service extractions over commits `bb0d6dc..fda2268`. WorkspaceService deliberately skipped (justification below).

### Phase 3 summary table

| Sub | Service | Commit | App.go Δ | Funcs collapsed | main.go wiring Δ |
|---|---|---|---|---|---|
| 3a | `ReactionService` | `bb0d6dc` | −22 | 4 → 1 | 2 → 1 |
| 3b | `ThreadService` | `b06e807` | −50 | 6 → 1 | 6 → 1 |
| 3c | `MessageService` | `3d53589` | −25 | 5 → 1 | 5 → 1 |
| 3d | `ChannelService` | `fda2268` | −82 | 9 → 1 | 9 → 1 |
| — | **Totals** | — | **−179** | **24 → 4** | **22 → 4** |

### Patterns that emerged during Phase 3

1. **Arity-based constructor shape.** Services with ≤4 methods use positional `NewXxxService(fn1, fn2, fn3)` (ReactionService). Services with 5+ methods use struct-of-funcs `NewXxxService(XxxServiceFuncs{Fetch: fn, Mark: fn, ...})` — lets tests omit unused fields without trailing nils and lets readers see what each closure is doing at the call site (Thread, Message, Channel).

2. **Adapter pattern with nil-safe operations.** Each interface method on the adapter checks its underlying func for nil before calling. Eliminated ~26 per-call-site nil guards across Update arms (ReactionService 9, ThreadService 12, MessageService 5, ChannelService 10).

3. **No-op service as `NewApp` default.** `noopXxxService` package-level constant wired by `NewApp` so call sites can dispatch without nil-checks even when no service has been registered (typical in tests that don't exercise a particular feature). `SetXxxService` overrides.

4. **Test-only helpers in `_test.go` file.** `internal/ui/services_helpers_test.go` defines per-method helper methods on App (e.g. `setChannelFetcherForTest`) that wire ONE closure each. `_test.go` suffix makes them invisible outside the test binary. Preserves the pre-Phase-3 test API (one-line `a.SetXxx(fn)`) without polluting production code.

5. **Read-modify-write test helpers (ChannelService specifically).** Many tests chain 3-4 `SetChannelXxx` calls in setup. Naive per-method helpers would overwrite previously-set funcs. Solution: `channelFuncsForTest(a)` unwraps the current adapter, helpers modify ONE field, then call `SetChannelService(NewChannelService(fns))`. Chained calls compose instead of overwriting.

### Why WorkspaceService was skipped

Three remaining callbacks could be grouped as a "WorkspaceService":

| Callback | Concern |
|---|---|
| `workspaceSwitcher` | switch active workspace |
| `themeSaveFn` | persist theme selection |
| `setStatusFn` | change my presence/DND |

Unlike the 4 services that shipped (each operating on a coherent domain object — channel, message, thread, reaction), these 3 callbacks share no domain object, no state, no invariant. The "workspace" linkage is purely "they all touch the active workspace somehow" — too loose for a cohesive service.

The plan's explicit non-goal #3:

> No "manager" / "service" classes that just rename methods. Each extraction must reduce App's field count AND own its tests.

WorkspaceService would be a 3-field → 1-field rename with no shared invariant. Per the criterion, **kept as 3 individual setters**.

### Remaining individual setters (deliberate; no further consolidation planned)

App still has ~24 individual `Set*` methods, of which ~4 are workspace-scoped callbacks (the WorkspaceService candidates above + `typingSendFn`). The remaining ~20 are **data setters**, not collaborator callbacks (SetWorkspaces, SetChannels, SetUserNames, SetCustomEmoji, SetCurrentUserID, SetThemeItems, SetImageFetcher, etc.). These are the App's "push data in" API, not the "wire in collaborators" API — different concern, not a Phase 3 target.

### Verification after Phase 3

- `go vet ./...` clean · `go build ./...` clean
- 39/39 packages green
- All reaction-click, thread-open, copy-permalink, mark-unread, channel-selected tier-rendering, nav-history tests green
- View benchmarks healthy (no regression)

---

## Phase 4 — Reducer split (OCP)

**Goal:** Break the giant `Update` switch (~1,600 lines, 75 message cases) into per-feature reducer files. App's `Update` stays as a thin dispatcher; each reducer owns a cohesive subset of message types. Adding a new message type then becomes "add to the relevant reducer's table" instead of "edit the giant switch."

**Status:** **NOT STARTED.**

**Proposed reducer families (rough sketch):**

| File | Owns these message types |
|---|---|
| `reducer_channels.go` | `ChannelSelectedMsg`, `MessagesLoadedMsg`, `OlderMessagesLoadedMsg`, `NewMessageMsg`, `ChannelMarkedReadMsg`, `ChannelMarkedRemoteMsg`, `WSMessageDeletedMsg` |
| `reducer_send.go` | `SendMessageMsg`, `MessageSentMsg`, `MessageSendFailedMsg`, `EditMessageMsg`, `MessageEditedMsg`, `DeleteMessageMsg`, `MessageDeletedMsg`, `MarkUnreadMsg`, `MessageMarkedUnreadMsg` |
| `reducer_threads.go` | thread / threads-list / debounce / mark messages |
| `reducer_reactions.go` | reaction add/remove/sent |
| `reducer_workspace.go` | `WorkspaceReadyMsg`, `WorkspaceSwitchedMsg`, `WorkspaceFailedMsg`, `ConversationOpenedMsg`, `SectionsRefreshedMsg`, `ChannelMembershipMsg`, `DMNameResolvedMsg`, `UserResolvedMsg`, `UserExternalMsg`, `CustomEmojisLoadedMsg`, `BrowseableChannelsLoadedMsg` |
| `reducer_presence.go` | `PresenceChangeMsg`, `StatusChangeMsg`, `DNDTickMsg`, `ToastMsg` |
| `reducer_mouse.go` | `MouseClickMsg`, `MouseMotionMsg`, `MouseReleaseMsg`, `MouseWheelMsg`, `autoScrollTickMsg` |
| `reducer_status.go` | all `statusbar.*` toast / failure messages |
| `reducer_upload.go` | `PasteMsg`, `UploadProgressMsg`, `UploadResultMsg` |
| `reducer_preview.go` | `previewLoadedMsg`, `previewErrorMsg`, `previewSpinnerTickMsg`, `messages.OpenImagePreviewMsg` |

**Dispatch table approach:**

```go
type reducer func(*App, tea.Msg) tea.Cmd

var reducers = map[reflect.Type]reducer{
    reflect.TypeOf(ChannelSelectedMsg{}): reduceChannelSelected,
    // ...
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if cmd, handled := a.preview.HandleMessage(msg); handled { return a, cmd }
    if cmd, handled := a.modeRouter.Route(msg); handled { return a, cmd }
    if fn, ok := reducers[reflect.TypeOf(msg)]; ok {
        return a, fn(a, msg)
    }
    return a, nil
}
```

(Reflection overhead is negligible compared to lipgloss rendering, but a typed switch dispatcher is also fine — both shapes are open to new messages without modifying core.)

**Stop-and-reassess rule:** If reducers 1-3 don't yield clear readability/maintainability wins, **stop**. The current monolithic switch is uncomfortable but not broken; a half-finished reducer split is worse than either extreme.

**Expected gain:** `Update` from ~1,600 lines to ~40. Each reducer file is independently reviewable (~150-400 lines). Adding a message type touches one file.

---

## Phase 5 — Mode handler strategy

**Goal:** Convert `handleNormalMode`, `handleInsertMode`, `handleChannelFinderMode`, etc. (currently a switch in `handleKey`) into a `ModeHandler` interface with a registration table.

**Status:** **NOT STARTED.**

```go
type ModeHandler interface {
    Key(a *App, msg tea.KeyMsg) tea.Cmd
}

var modeHandlers = map[Mode]ModeHandler{
    ModeNormal:               normalModeHandler{},
    ModeInsert:               insertModeHandler{},
    ModeCommand:              commandModeHandler{},
    ModeChannelFinder:        channelFinderModeHandler{},
    ModeReactionPicker:       reactionPickerModeHandler{},
    ModeConfirm:              confirmModeHandler{},
    ModeWorkspaceFinder:      workspaceFinderModeHandler{},
    ModeThemeSwitcher:        themeSwitcherModeHandler{},
    ModePresenceMenu:         presenceMenuModeHandler{},
    ModePresenceCustomSnooze: presenceCustomSnoozeModeHandler{},
    ModeHelp:                 helpModeHandler{},
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
    if h, ok := modeHandlers[a.mode]; ok {
        return h.Key(a, msg)
    }
    return nil
}
```

**Expected gain:** `handleKey` from ~50 lines to ~6. Adding/removing modes is a one-line registration. Each mode handler is an isolated type that can be unit-tested without exercising the full App.

---

## Phase 6 — View region split

**Goal:** Extract per-region renderers from `View()` (~470 lines) so View becomes ~80 lines of composition.

**Status:** **NOT STARTED.**

**Per-region functions (rough sketch):**

- `(a *App) renderRail(width, height int) string`
- `(a *App) renderSidebar(width, height int) string`
- `(a *App) renderMessagesRegion(frame panelLayoutFrame) string` — handles both ViewChannels and ViewThreads
- `(a *App) renderThreadRegion(frame panelLayoutFrame) string`
- `(a *App) renderStatusbar(width int) string`
- `(a *App) renderComposeRow(width int) string`
- `(a *App) renderTypingRow(width int) string`

View becomes a composition:

```go
func (a *App) View() tea.View {
    // early-out for unmeasured terminal (loading-overlay fallback)
    if a.width == 0 || a.height == 0 { ... }

    frame := a.layout.Compute(...)
    if frame.ThreadAutoHidden { ... }

    rail     := a.renderRail(frame.RailWidth, frame.ContentHeight)
    sidebar  := a.renderSidebar(frame.SidebarWidth, frame.ContentHeight)
    msgRegion := a.renderMessagesRegion(frame)
    threadRegion := a.renderThreadRegion(frame)
    statusbar := a.renderStatusbar(a.width - frame.RailWidth)

    content := lipgloss.JoinHorizontal(...)
    return tea.NewView(lipgloss.JoinVertical(content, statusbar))
}
```

**Pre-condition:** The View benchmark (`BenchmarkAppViewCompose`/`BenchmarkAppViewIdle`) must stay healthy through this phase. Each extraction step should run the benchmark and compare to the Phase 2 baseline.

**Expected gain:** View readable in a single screen. Per-region renderers are independently testable (golden-string tests at fixed widths/heights).

---

## Phase 7 — Tighten types (Primitive Obsession)

**Goal:** Introduce ID types for the strings that are passed around everywhere.

**Status:** **NOT STARTED — DEFERRED.** Lowest priority, largest blast radius.

```go
type ChannelID string
type TeamID    string
type ThreadTS  string
type UserID    string
type MessageTS string
```

**Why deferred:** This touches every package boundary (messages, sidebar, thread, channelfinder, cache, slack/...) and every call site of the new service interfaces from Phase 3. Worth doing eventually for the bug class it catches (channelID/teamID swap, threadTS in a channelID slot), but only after Phases 3-6 have settled the public surfaces.

---

## What I'm NOT doing

Explicit non-goals to keep the scope honest:

- **No wrapping every primitive in value objects.** TUI ID strings aren't `Money`/`Email`; the safety win is real but small. Phase 7 only.
- **No premature interface for sub-models** (`messages.Model`, `thread.Model`, etc.). They're already cohesive; mocking via interfaces buys little.
- **No "manager" / "service" classes that just rename methods.** Each extraction must reduce App's field count AND own its tests.
- **No DI framework.** Plain Go struct embedding / constructor options.
- **No big-bang.** Each phase ships green. If Phase 4 reducers don't yield clear wins by reducer 3, stop and reassess.

---

## Branch and commit topology

```
main (f2defed = merged #26 scroll improvements)
 │
 ├── refactor/app-phase-0-characterization-tests
 │      └── c5653b1  phase 0 — characterization tests
 │
 ├── refactor/app-phase-1-extract-msgs-callbacks
 │      └── da1f53b  phase 1 — extract msgs and callbacks
 │
 └── refactor/app-phase-2-extract-state-objects  (tip; carries Phases 2+3)
        ├── def1ca5  phase 2a — navHistoryStore
        ├── b1ee302  phase 2b — selfSendDedup
        ├── 9c7961f  phase 2c — workspaceBootstrap
        ├── 1185045  phase 2d — typing tracker + broadcaster
        ├── 7ca4a80  phase 2e — editController
        ├── c00033a  phase 2f — presenceController
        ├── 4a7747f  phase 2g — panelRenderCache
        ├── 6ee5654  phase 2h — dragSelection
        ├── 5523019  phase 2i — imagePreviewController
        ├── 036555a  phase 2j — panelLayout
        ├── 30bcb19  docs — write plan
        ├── bb0d6dc  phase 3a — ReactionService
        ├── b06e807  phase 3b — ThreadService
        ├── 3d53589  phase 3c — MessageService
        └── fda2268  phase 3d — ChannelService
```

Phase 0/1 branches still point to their pre-rebase commits. If they need to be PR'd separately to main, re-rebase them onto current main first. The tip branch's name is now slightly stale (it carries 3+ phases) but the contents are unambiguous.

---

## How to resume

If picking this up in a new session:

1. `git checkout refactor/app-phase-2-extract-state-objects` (or whatever the current tip branch is).
2. `git fetch origin && git log --oneline HEAD..origin/main` — check for upstream drift.
3. If there are new commits on main, rebase: `git rebase origin/main` (the work has rebased cleanly through one main update already; the conflict surface for any future drift is concentrated in `app.go`'s Update arms and the field block).
4. Read this doc + skim the most recent phase's commit message for context.
5. Pick the next phase from the "NOT STARTED" set above. **Phase 4 (reducer split)** is the natural continuation — the giant `Update` switch (~1,600 lines, 75 message cases) is the single biggest remaining concentration of complexity in `app.go`.
