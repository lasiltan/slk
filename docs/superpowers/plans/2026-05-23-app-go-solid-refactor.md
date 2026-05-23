# internal/ui/app.go SOLID Refactor — Implementation Plan

> **Status:** Phases 0–5 complete (10 state extractions + 4 service interfaces + 12 reducer migrations + 11 mode-handler extractions). Phases 6–7 ahead.
> **Branch:** `refactor/app-phase-2-extract-state-objects` (tip carries Phases 2+3+4+5; earlier phases on their own branches off main).
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

| | Original | After Phase 2 | After Phase 3 | After Phase 4 | After Phase 5 | Δ from original |
|---|---|---|---|---|---|---|
| `app.go` lines | 6,216 | 5,099 | 4,920 | 3,434 | **2,733** | **−3,483 (−56.0%)** |
| `Update` body lines | ~1,571 | ~1,571 | ~1,571 | ~85 | ~85 | **−1,486 (−94.6%)** |
| `handleKey` mode switch | ~24 lines | ~24 | ~24 | ~24 | **1** (single map lookup) | **−23 (−95.8%)** |
| `handle*Mode` methods on App | 11 (~700 lines) | 11 | 11 | 11 | **0** | all moved to per-mode files |
| `App` struct fields | ~95 | ~60 | ~40 | ~40 | ~40 | ~−55, consolidated into 10 controllers + 4 service interfaces |
| `App` callback `Set*` methods | ~28 | ~28 | 4 | 4 | 4 | **−24** (24 collapsed into 4 service setters) |
| main.go wiring calls | ~40 | ~40 | ~20 | ~20 | ~20 | **−20** |
| Cohesive new files under `internal/ui/` | 0 | 12 | 14 | 22 | **34** | + mode_handlers.go + 11 mode_*.go + test shim (Phase 5) |

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

**Goal:** Break the giant `Update` switch (~1,571 lines, ~80 message cases) into per-feature reducer files. App's `Update` stays as a thin dispatcher; each reducer owns a cohesive subset of message types. Adding a new message type then becomes "add a case to the relevant reducer's switch" instead of "edit the giant switch."

**Status:** **COMPLETE** — 12 reducer sub-phases (4a–4m, with 4f deliberately skipped) over commits `f18c4ed..aa2a504`.

### Phase 4 design choices (decided at start)

Three design questions were resolved before any code moved:

1. **Dispatch shape: per-reducer typed switch + chain-of-responsibility.** Each reducer is a value implementing `Handle(a *App, msg tea.Msg) (tea.Cmd, bool)`; the chain is a variadic call `dispatchReducers(a, msg, a.presence, a.preview, ...)` declared inline in `Update`. Chosen over a `map[reflect.Type]reducer` because it preserves compile-time exhaustiveness and avoids the per-dispatch reflection allocation. Reducers return `(cmd, true)` when they own the message, `(nil, false)` to pass.

2. **Owner-absorbed where possible.** When a message family belongs to a controller already extracted in Phase 2 (presence, preview, drag, typing, bootstrap), the reducer is a method on that controller — state and behavior co-located. When no single owner exists (channels, threads, send, reactions, workspace, mouse, IO/toasts), the reducer is a free `reducerFunc` literal in a per-family `reducer_*.go` file.

3. **Smoke tests via existing coverage.** Existing characterization tests (Phase 0) already dispatch messages through `a.Update(...)`. Once a reducer is wired in, those tests automatically exercise the new path; if the wiring is wrong (reducer not registered, type assertion mismatched), the existing assertions fail. No new smoke tests were added; the existing 24-package suite serves as the regression gate.

### Phase 4 summary table

| Sub | Reducer | Commit | Δ app.go | Arms migrated | Owner |
|---|---|---|---|---|---|
| 4a | `presence.Handle` | `f18c4ed` | −22 | 3 (PresenceChange, StatusChange, DNDTick) | `presenceController` (Phase 2f) |
| 4b | `preview.Handle` | `1974b31` | −54 | 4 (OpenImagePreview, previewSpinnerTick, previewLoaded, previewError) | `imagePreviewController` (Phase 2i) |
| 4c | `drag.Handle` | `ce9d620` | −111 | 3 (MouseMotion, autoScrollTick, MouseRelease) | `dragSelection` (Phase 2h) |
| 4d | `typing.Handle` | `c9bdf7f` | −17 | 2 (UserTyping, TypingExpired) | `typingTracker` (Phase 2d) |
| 4e | `bootstrap.Handle` | `e67d7ab` | −9 | 3 (SpinnerTick, LoadingTimeout, WorkspaceFailed) | `workspaceBootstrap` (Phase 2c) |
| 4f | — | **SKIPPED** | — | — | `editController` only had 1 related arm (`MessageEditedMsg`); moved into 4i instead |
| 4g | `reduceReactions` | `d2dfc13` | −13 | 3 (ReactionAdded, ReactionRemoved, ReactionSent) | free reducer |
| 4h | `reduceThreads` | `dfdf9a4` | −173 | 9 (ThreadMarkedRemote, threadFetchDebounce, ThreadRepliesLoaded, ThreadsViewActivated, ThreadsListLoaded/Dirty, SendThreadReply, ThreadReplySent/Failed) | free reducer |
| 4i | `reduceSend` | `fcd510e` | −264 | 11 (NewMessage, SendMessage, MessageSent/SendFailed, EditMessage, MessageEdited, DeleteMessage, MessageDeleted, MarkUnread, MessageMarkedUnread, WSMessageDeleted) | free reducer |
| 4j | `reduceChannels` | `5751271` | −208 | 9 (ChannelSelected, MessagesLoaded, OlderMessagesLoaded, ChannelMarkedRemote/Read, ChannelMembership, ChannelJoined/Failed, BrowseableChannelsLoaded) | free reducer |
| 4k | `reduceWorkspace` | `9b99659` | −208 | 9 (WorkspaceReady/Switched, ConversationOpened, SectionsRefreshed, DMNameResolved, UserResolved, UserExternal, ReadStateChanged, CustomEmojisLoaded) | free reducer |
| 4l | `reduceIO` | `300adcd` | −213 | 17 (PasteMsg, UploadProgress/Result, ConnectionState, ToastMsg, editEmptyToast, 3 image/avatar arms, 9 statusbar.* toasts) | free reducer |
| 4m | `reduceMouse` | `aa2a504` | −199 | 2 (MouseWheel, MouseClick) | free reducer |
| — | **Totals** | — | **−1,491** | **75 arms** | 5 controller-absorbed + 7 free reducers |

### Files added during Phase 4

- `internal/ui/reducers.go` (79 lines) — `reducer` interface + `reducerFunc` adapter + `dispatchReducers` chain
- `internal/ui/reducer_reactions.go` (53)
- `internal/ui/reducer_threads.go` (254)
- `internal/ui/reducer_send.go` (364)
- `internal/ui/reducer_channels.go` (297)
- `internal/ui/reducer_workspace.go` (308)
- `internal/ui/reducer_io.go` (235)
- `internal/ui/reducer_mouse.go` (264)

Plus `Handle` methods + tick-cmd helpers added to existing controllers: `presence.go` (+73), `imagepreview.go` (+92), `drag.go` (+167), `typing.go` (+47), `bootstrap.go` (+53).

### Patterns that emerged during Phase 4

Worth naming because they recurred across sub-phases:

1. **Tick-cmd helper next to FSM state.** `previewSpinnerTickCmd` (4b), `autoScrollTickCmd` (4c), `typingExpiredTickCmd` (4d), `spinnerTickCmd` (4e). Each reducer that reschedules its own `tea.Tick(...)` chain gets a named helper, eliminating ~6 duplicates of the same inline `tea.Tick(N*time.Second, func(time.Time) tea.Msg { return XxxTickMsg{} })` literal scattered across `app.go`. One source of truth per chain; the chain's cadence becomes a named constant in the same file.

2. **Big-arm helper extraction.** `reduceNewMessage` / `reduceSendMessage` (4i), `reduceChannelSelected` (4j), `reduceWorkspaceReady` / `reduceWorkspaceSwitched` (4k), `reduceMouseWheel` / `reduceMouseClick` (4m), `reducePaste` (4l). Any reducer arm over ~50 lines or with three or more nested decision branches gets extracted into a package-private helper sitting next to the dispatcher. Keeps the top-level switch readable as a dispatch table.

3. **Toast-with-clear collapse (4l).** The 11 statusbar toast arms each did `a.statusbar.SetToast(text)` + `cmds = append(cmds, tea.Tick(Ns, ... CopiedClearMsg{}))`. Collapsed into two helpers: `copiedClearAfter(d)` returns the tick cmd; `toastWithClear(a, text, d)` does both and returns the cmd. ~70 lines of repetitive `cmds = append` collapsed into one-liners.

4. **Free reducers for cross-cutting families.** When a message family touches 4+ sub-models (channel-select touches sidebar/messagepane/threadPanel/compose/threadCompose/channelFinder/navHistory/statusbar/etc.), no single existing controller is a sensible owner. The reducer lives in its own file and is registered as a `reducerFunc` literal. This is the same heuristic Phase 3 used to skip `WorkspaceService`: "rename without ownership" is worse than "free function with cohesion."

5. **Editor-cancel guard via `editing.Matches`.** Already established in Phase 2e, but Phase 4 made it visible at three more sites: `MessageEditedMsg`, `WSMessageDeletedMsg`, both inside `reduceSend`. Each reads `if a.editing.Matches(channel, ts) { a.cancelEdit() }` — a single named query replacing the three-clause guard that would otherwise repeat.

### Verification after Phase 4

- `go vet ./...` clean · `go build ./...` clean
- 39/39 packages green, 24/24 in `internal/ui/...`
- View benchmarks healthy: `BenchmarkAppViewCompose ~4.8ms` (Phase 3 baseline ~4.7ms on same machine), `BenchmarkAppViewIdle ~1.89ms` (unchanged). No measurable regression. Initial single-iteration runs showed a ~30% Compose blip; with 3-iteration warm-up the steady-state is within noise.
- All Phase 0 characterization tests green; existing test suite serves as the dispatch-wiring smoke test (any reducer accidentally not registered would fail the corresponding `a.Update(...)`-based tests).
- All upstream-merged tests (click-opens-thread, scroll-decouple, wheel-scroll-config, thread-parent-scrolls) green.

### Final shape of `App.Update`

```go
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    // Phase 4 reducer chain. First reducer to claim the message
    // short-circuits the rest; unclaimed messages fall through to
    // the residual switch.
    if cmd, handled := dispatchReducers(a, msg,
        a.presence,
        a.preview,
        a.drag,
        a.typing,
        a.bootstrap,
        reduceReactions,
        reduceThreads,
        reduceSend,
        reduceChannels,
        reduceWorkspace,
        reduceIO,
        reduceMouse,
    ); handled {
        if cmd != nil {
            cmds = append(cmds, cmd)
        }
        return a, tea.Batch(cmds...)
    }

    // Image-preview modal key-trap (pre-switch interception).
    if a.preview.Active() { ... }

    // Residual: only WindowSize + KeyMsg now live here.
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        a.width, a.height = msg.Width, msg.Height
        return a, nil
    case tea.KeyMsg:
        cmd := a.handleKey(msg)
        if cmd != nil { cmds = append(cmds, cmd) }
    }
    return a, tea.Batch(cmds...)
}
```

~85 lines including comments, down from ~1,571. Adding a new message family is now a one-line `reduce*` append to the variadic chain plus a new `reducer_*.go` file — strictly open/closed.

---

## Phase 5 — Mode handler strategy

**Goal:** Convert `handleNormalMode`, `handleInsertMode`, `handleChannelFinderMode`, etc. (currently a switch in `handleKey`) into a dispatch table with a per-mode file for each handler.

**Status:** **COMPLETE** — 12 sub-phases (5a–5l) over commits `6923cd7..b7f45b0`.

### Phase 5 design choices (decided at start)

1. **Function-type + map, not interface + struct.** `type modeHandler func(*App, tea.KeyMsg) tea.Cmd; var modeHandlers = map[Mode]modeHandler{...}`. Mirrors the Phase 4 `reducerFunc` adapter. Zero per-mode boilerplate; the free-function signature reads identically to the receiver form (`a *App` is the first parameter either way).
2. **One file per mode** (e.g. `mode_normal.go`, `mode_insert.go`). Consistency over size optimization — even the 21-line `mode_help.go` and 21-line `mode_command.go` get their own file so the "one mode = one file" grep affordance applies uniformly.
3. **Same per-sub-phase commit cadence as Phase 4.**

### Phase 5 summary table

| Sub | Mode | Commit | Δ app.go | Handler lines |
|---|---|---|---|---|
| 5a | (mechanism) `modeHandlers` map + `dispatchModeKey` | `6923cd7` | −22 | new file 80 lines |
| 5b | Command | `edd5d30` | −5 | 21 |
| 5c | Help | `933ae44` | −22 | 33 |
| 5d | PresenceCustomSnooze | `cb7b09e` | −29 | 51 |
| 5e | WorkspaceFinder | `ef5830f` | −33 | 47 |
| 5f | PresenceMenu | `cc01966` | −40 | 59 |
| 5g | ThemeSwitcher | `9269375` | −40 | 60 |
| 5h | ChannelFinder | `8127e8c` | −50 | 67 |
| 5i | ReactionPicker | `46a9475` | −58 | 80 |
| 5j | Confirm | `17aadca` | −16 | 30 |
| 5k | Normal | `685e127` | −208 | 233 |
| 5l | Insert | `b7f45b0` | −206 | 248 |
| — | **Totals** | — | **−729** | 1,009 lines moved into 11 per-mode files |

### Files added during Phase 5

- `internal/ui/mode_handlers.go` (80 lines) — `modeHandler` type + `modeHandlers` map + `dispatchModeKey`
- `internal/ui/mode_command.go` (21)
- `internal/ui/mode_help.go` (33)
- `internal/ui/mode_presence_snooze.go` (51)
- `internal/ui/mode_workspace_finder.go` (47)
- `internal/ui/mode_presence_menu.go` (59)
- `internal/ui/mode_theme_switcher.go` (60)
- `internal/ui/mode_channel_finder.go` (67)
- `internal/ui/mode_reaction_picker.go` (80)
- `internal/ui/mode_confirm.go` (30)
- `internal/ui/mode_normal.go` (233)
- `internal/ui/mode_insert.go` (248)
- `internal/ui/mode_handlers_helpers_test.go` (35) — test-only method shims (`func (a *App) handleXxxMode(...) tea.Cmd { return handleXxxMode(a, msg) }`) preserving the pre-Phase-5 test API for 4 handlers (`handleChannelFinderMode`, `handleConfirmMode`, `handleNormalMode`, `handleInsertMode`) that had existing test call sites. Mirrors the `services_helpers_test.go` pattern from Phase 3.

### Patterns that emerged during Phase 5

1. **Method-value bootstrap, free-function final state.** Phase 5a populates `modeHandlers` with method values (`(*App).handleNormalMode`, etc.) so the dispatch mechanism lands without any file moves. Phases 5b–5l then swap each entry from a method value to a free function as the body migrates to its own file. The dispatcher contract is unchanged for the entire migration; only the map entries change shape.

2. **Test-only shim file for migrated methods.** Phase 4 services and Phase 5 modes both followed the same rule: when a production method is moved to a free function but tests still reference the method form, add a `func (a *App) handleXxxMode(msg)` shim in a `_test.go` file that delegates to the free function. Production code calls the free function directly; tests keep their pre-refactor API. The `_test.go` suffix keeps shims invisible outside the test binary.

3. **Per-mode file naming uses snake_case for multi-word modes.** `mode_workspace_finder.go`, `mode_presence_snooze.go`, etc. Mirrors the existing `app_xxx_test.go` characterization-test naming from Phase 0.

4. **Compile-time signature anchor.** `mode_handlers.go` has a single `var _ modeHandler = handleNormalMode` at the bottom. If a future change to a handler signature drifts away from the `modeHandler` type, the map literal would still compile (Go's map literal values are checked one at a time) but this anchor catches the drift on the canonical handler. Mirrors the `var _ reducer = ...` pattern from Phase 4.

### Verification after Phase 5

- `go vet ./...` clean · `go build ./...` clean
- 39/39 packages green, 24/24 in `internal/ui/...`
- View benchmarks (3-iteration steady-state):
  - `BenchmarkAppViewCompose ~4.7ms` (Phase 4 was ~4.8ms; within noise, no regression)
  - `BenchmarkAppViewIdle ~1.88ms` (unchanged)
- Existing characterization tests cover the new dispatcher via 33 `app.handleXxxMode(...)` call sites that route through the test shim file → free function. If any free function were never registered in `modeHandlers` map or had a stale shim, the existing tests would surface the regression.

### One mid-Phase fix worth noting

Phase 5j initially shipped with the new `mode_confirm.go` file missing from the commit (a `git add -u` instead of `git add` of the new untracked file); Phase 5k swept it up but left 5j broken in isolation (would fail `git bisect`). Fixed via an interactive `rebase -i 46a9475` with an `edit` action on the 5j commit, then re-creating `mode_confirm.go` and `git commit --amend`. 5j now builds and tests standalone (verified at `17aadca`).

### Final shape of `handleKey`

```go
func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
    if key.Matches(msg, a.keys.Quit) {
        if a.mode != ModeConfirm {
            a.openQuitConfirm()
        }
        return nil
    }
    if a.bootstrap.IsLoading() {
        return nil
    }
    return dispatchModeKey(a, msg)
}
```

13 lines including the global Quit handler and the loading-bootstrap gate. The 24-line mode switch is now a single `dispatchModeKey` call.

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
 └── refactor/app-phase-2-extract-state-objects  (tip; carries Phases 2+3+4)
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
        ├── fda2268  phase 3d — ChannelService
        ├── 717c413  docs — phase 3 complete
        ├── f18c4ed  phase 4a — presence reducer + dispatch chain
        ├── 1974b31  phase 4b — image preview reducer
        ├── ce9d620  phase 4c — drag-FSM reducer
        ├── c9bdf7f  phase 4d — typing reducer
        ├── e67d7ab  phase 4e — bootstrap reducer
        ├── d2dfc13  phase 4g — reactions reducer (first free reducer)
        ├── dfdf9a4  phase 4h — threads reducer
        ├── fcd510e  phase 4i — message-lifecycle reducer
        ├── 5751271  phase 4j — channels reducer
        ├── 9b99659  phase 4k — workspace reducer
        ├── 300adcd  phase 4l — IO / toast / asset-loading reducer
        ├── aa2a504  phase 4m — mouse router reducer (final)
        ├── 904cddc  docs — phase 4 complete
        ├── 6923cd7  phase 5a — mode handler dispatch table
        ├── edd5d30  phase 5b — Command mode
        ├── 933ae44  phase 5c — Help mode
        ├── cb7b09e  phase 5d — PresenceCustomSnooze mode
        ├── ef5830f  phase 5e — WorkspaceFinder mode
        ├── cc01966  phase 5f — PresenceMenu mode
        ├── 9269375  phase 5g — ThemeSwitcher mode
        ├── 8127e8c  phase 5h — ChannelFinder mode
        ├── 46a9475  phase 5i — ReactionPicker mode
        ├── 17aadca  phase 5j — Confirm mode (amended via rebase)
        ├── 685e127  phase 5k — Normal mode
        └── b7f45b0  phase 5l — Insert mode (final)
```

Phase 4f was deliberately skipped (see Phase 4 summary table for rationale). Phase 0/1 branches still point to their pre-rebase commits. If they need to be PR'd separately to main, re-rebase them onto current main first. The tip branch's name is now stale (it carries Phases 2+3+4+5, ~42 commits) but the contents are unambiguous.

---

## How to resume

If picking this up in a new session:

1. `git checkout refactor/app-phase-2-extract-state-objects` (or whatever the current tip branch is).
2. `git fetch origin && git log --oneline HEAD..origin/main` — check for upstream drift.
3. If there are new commits on main, rebase: `git rebase origin/main`. The conflict surface for any future drift is concentrated in `app.go`'s remaining handler bodies and the per-reducer files in `internal/ui/reducer_*.go`; an upstream change that added a new message arm would land in the `Update` switch where the matching reducer's `Handle` method now is.
4. Read this doc + skim the most recent phase's commit message for context.
5. Pick the next phase from the "NOT STARTED" set above. **Phase 6 (View region split)** is the natural continuation — `View()` is ~470 lines of composition logic that can be broken into per-region renderers (rail, sidebar, messages, thread, statusbar, compose, typing). Phase 7 (Primitive Obsession / ID types) is the other remaining item; it touches every package boundary so it's lowest priority.
