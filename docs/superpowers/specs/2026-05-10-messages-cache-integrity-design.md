# Messages cache integrity and workspace state correctness

## Problem

When the user switches workspaces and returns to one they were previously
viewing, the messages pane briefly displays older messages and then,
roughly one to three seconds later, repaints with newer content. Switching
between channels inside one workspace doesn't exhibit the same flicker.

Investigation of `slk-debug.log` from a reproducing session traces this
to three compounding issues.

### 1. Bootstrap race between WorkspaceReady events

`internal/ui/app.go:WorkspaceReadyMsg` (lines 2098-2147) auto-selects the
first channel of a workspace only when `a.activeChannelID == ""`. The
`ChannelSelectedMsg` that would set `activeChannelID` is queued in `cmds`,
not run inline. When two `WorkspaceReadyMsg`s arrive in the same Bubble
Tea Update cycle, both observe `activeChannelID == ""` and both queue an
auto-select. The second one wins (because `a.activeTeamID = msg.TeamID`
runs immediately), but `cmd/slk/main.go:986-989` only invokes
`wireCallbacks(wctx)` for whichever workspace was first to `claimActive`
under the connect-goroutine race. App-state and main.go-state diverge:
`app.activeTeamID` points to workspace B but `channelFetcher` is bound to
workspace A's client. Every `client.GetHistory` for a workspace B channel
returns `channel_not_found`, the cache is never refreshed, and stale data
sits in SQLite until the user manually switches workspaces (which calls
`wireCallbacks` correctly).

The debug log confirms: at `10:09:17.908`, `MessagesLoadedMsg
channel=C04T4TH9N active=C04T4TH9N kind=nil_keep_cache count=0` — the
fetch returned nil because Slack returned `channel_not_found` six lines
earlier.

### 2. Closure-rebinding pattern is fragile

`cmd/slk/main.go:wireCallbacks(wctx)` rebuilds ten App callbacks
(`SetChannelFetcher`, `SetChannelCacheReader`, `SetMessageSender`,
`SetMessageEditor`, …) on every workspace switch. Each closure captures
`wctx.Client`, `wctx.UserNames`, `wctx.LastReadMap`, etc. at construction
time. Three problems:

- If a fetch started under workspace A is in flight when the user switches
  to workspace B and back to A, the closure now in `App.channelFetcher`
  is the latest A-binding (correct) — but any *in-flight* fetch from
  before the switch already captured the right client. This works today
  only by luck of how Go closures evaluate.
- The bootstrap race above only exists because `wireCallbacks` is called
  inside the per-workspace connect goroutine instead of once at startup.
- Adding a new workspace-scoped callback means modifying `wireCallbacks`
  and every call site that rebinds it. The pattern doesn't scale.

### 3. Cache freshness is undefined

`internal/cache/messages.go:GetMessages` returns the last 50 top-level
rows ordered by `ts ASC`. There is no concept of how old that snapshot
is. `loadCachedMessages` in main.go reads the rows; `ChannelSelectedMsg`
in app.go renders them instantly and fires a background fetch
unconditionally. The user always sees `[stale cache] → ~2-3 s wait →
[fresh data]` on every channel select, including the first visit of a
session.

The 2-3 s wait is dominated not by Slack but by **synchronous user
resolution** inside the message-processing loop. `resolveUser`
(`cmd/slk/main.go:1458-1503`) calls `client.GetUserProfile(userID)`
synchronously for every unknown author *and* for every known author
whose avatar isn't yet cached. A 50-message history with N unknown
authors costs N sequential round-trips. The debug log shows Slack's
`GetConversationHistory` returns in ~270 ms, but the fetcher takes
~2470 ms total — the difference is the user-resolution loop.

### 4. Visible secondary waste in WorkspaceSwitchedMsg

`internal/ui/app.go:2000-2004` does `SetMessages(nil) + spinner` before
queuing the `ChannelSelectedMsg` that will immediately call
`SetMessages(cached)` again. The user sees the pane go `[N old msgs] →
[empty + spinner] → [N cached msgs] → [N fresh msgs]` — three repaints,
three cursor relocations, all in under 100 ms.

## Goals

- A single global "active workspace" pointer with race-free reads. Every
  workspace-scoped callback reads it at invocation time, not at closure
  construction time. `wireCallbacks` runs exactly once.
- `WorkspaceReadyMsg` claims active deterministically via an
  `InitialActive` flag set in main.go's connect goroutine; only the
  workspace flagged `InitialActive: true` auto-selects a channel.
- Channel select decides among three tiers based on per-channel cache age:
  fresh (no fetch), recent (cache-first + verify), stale (spinner only).
- A subtle status-bar indicator visualizes the "verifying" tier so the
  user knows the displayed cache is being checked.
- The `WorkspaceSwitchedMsg` handler no longer wipes the pane —
  `ChannelSelectedMsg` paints over.
- Message-history processing stops blocking on per-user profile fetches.
  Unknown authors render as their user ID, a background resolver requests
  the profile, and a `UserResolvedMsg` patches the names live.

## Non-goals

- Splitting workspace callbacks into a per-team registry on the App
  side (the "active pointer" approach gets the same correctness without
  changing App's API surface).
- Bulk `users.info` batching. Async-per-user resolution is enough to cut
  the perceived delay from ~2.5 s to ~300 ms. Batching is a follow-up.
- WebP / image-decode fixes (out of scope; observed but unrelated).
- Rate-limit protection for Slack API calls. No queue introduced.
- Reconnection logic changes. The existing `OnDisconnect`/`OnConnect`
  hooks remain as-is; freshness tracking treats reconnections like any
  other gap (cache ages out the same way).
- Migration of every existing `log.Printf` call. Only the changed paths
  get new `debuglog.Cache` entries to keep the log signal clean.

## Architecture

### 1. Active-workspace pointer

A new type in `cmd/slk/main.go`:

```go
type workspaceRouter struct {
    active atomic.Pointer[WorkspaceContext]
    // all is mutated only during connect, before p.Run; safe to read
    // without a mutex once the program loop has started.
    all map[string]*WorkspaceContext
}

func (r *workspaceRouter) Active() *WorkspaceContext { return r.active.Load() }
func (r *workspaceRouter) Set(wctx *WorkspaceContext) { r.active.Store(wctx) }
func (r *workspaceRouter) ByID(teamID string) *WorkspaceContext {
    return r.all[teamID]
}
```

The single instance is constructed alongside `app` in `runApp` and
captured by `wireCallbacks(router)`. Every callback body starts with:

```go
wctx := router.Active()
if wctx == nil { return nil }
```

`wireCallbacks(router)` is invoked exactly once, before `p.Run()`. The
connect goroutine no longer calls it.

`app.SetWorkspaceSwitcher` becomes:

```go
app.SetWorkspaceSwitcher(func(teamID string) tea.Msg {
    wctx := router.ByID(teamID)
    if wctx == nil { return nil }
    router.Set(wctx)
    return ui.WorkspaceSwitchedMsg{...}
})
```

### 2. Bootstrap active-claim

The "is this workspace the initial active one?" decision rides on
`WorkspaceReadyMsg` itself, eliminating any cross-goroutine setter race:

```go
WorkspaceReadyMsg struct {
    ...                  // existing fields
    InitialActive bool   // true iff this workspace claims initial active
}
```

App gains one boolean (no setter needed):

```go
type App struct {
    ...
    bootstrapActiveClaimed bool
}
```

`WorkspaceReadyMsg` guard becomes:

```go
case WorkspaceReadyMsg:
    a.MarkWorkspaceReady(msg.TeamName)
    if msg.InitialActive && !a.bootstrapActiveClaimed {
        a.bootstrapActiveClaimed = true
        // existing setup: theme, channels, finder, currentUserID,
        // activeTeamID, statusByTeam, workspaceRail, then queue
        // ChannelSelectedMsg for first/restored channel.
    }
    // threadsListFetcher kick stays outside the if (per-workspace).
```

In `cmd/slk/main.go`, after `defaultTeamID` is resolved, the per-workspace
connect goroutine computes `InitialActive` deterministically:

```go
// Package-level state in main.go (alongside defaultTeamID):
var firstReady sync.Once

// Inside the connect goroutine, after wctx is built:
isInitial := false
if defaultTeamID != "" {
    if wctx.TeamID == defaultTeamID {
        isInitial = true
        router.Set(wctx)
    }
    // else: not the configured default; never claim.
} else {
    firstReady.Do(func() {
        isInitial = true
        router.Set(wctx)
    })
}
p.Send(ui.WorkspaceReadyMsg{
    ...,
    InitialActive: isInitial,
})
```

`sync.Once` and `defaultTeamID == ""` together ensure exactly one
workspace claims initial-active, regardless of connect ordering.

**Caveat:** If `defaultTeamID` is configured but that workspace never
connects (network error, auth failure), no workspace claims initial-
active and the UI stays on the empty default state. This matches today's
behavior and is out of scope. A timeout-based fallback is a follow-up.

### 3. Per-channel cache freshness

#### Schema change

Add a column to the existing `channels` table (`internal/cache/db.go`).
Follows the existing additive-migration pattern in `migrate()` using
`addColumnIfMissing` (lines 142-152 today):

```go
if err := db.addColumnIfMissing("channels", "synced_at",
    "ALTER TABLE channels ADD COLUMN synced_at INTEGER NOT NULL DEFAULT 0"); err != nil {
    return err
}
```

The default `0` makes every existing cached channel "never synced",
which falls into the spinner-only tier the first time it's visited
after upgrade — correct behavior.

#### Cache API additions

```go
// internal/cache/channels.go (or a new file)
func (db *DB) SetChannelSyncedAt(channelID string, unixSec int64) error
func (db *DB) GetChannelSyncedAt(channelID string) int64  // 0 if missing/never
```

#### Three-tier ChannelSelectedMsg

```go
const (
    cacheFreshThreshold = 30 * time.Second   // tier 1
    cacheStaleThreshold = 5 * time.Minute    // tier 2 → tier 3 boundary
)

age := time.Since(time.Unix(syncedAt, 0))

switch {
case syncedAt > 0 && age < cacheFreshThreshold:
    // Tier 1: render cache, no fetch, but DO mark-as-read for latest.
    a.messagepane.SetLoading(false)
    a.messagepane.SetMessages(cached)
    if len(cached) > 0 && a.channelReadMarker != nil {
        latestTS := cached[len(cached)-1].TS
        cmds = append(cmds, func() tea.Msg {
            return a.channelReadMarker(msg.ID, latestTS)
        })
    }
    // No fetcherCmd, no syncing indicator.

case syncedAt > 0 && age < cacheStaleThreshold:
    // Tier 2: cache-first + verify in background.
    a.messagepane.SetLoading(false)
    a.messagepane.SetMessages(cached)
    a.statusbar.SetSyncing(true)
    cmds = append(cmds, fetcherCmd)

default:
    // Tier 3: spinner only.
    a.messagepane.SetLoading(true)
    a.messagepane.SetMessages(nil)
    cmds = append(cmds, fetcherCmd, spinnerTickCmd)
}
```

Two new optional readers/callbacks on App:

- `SetChannelSyncedAtReader(func(channelID string) int64)` — wired in
  `wireCallbacks` to `db.GetChannelSyncedAt`. Nil reader (tests) defaults
  all channels to `synced_at = 0` and Tier 3.
- `SetChannelReadMarker(func(channelID, ts string) tea.Msg)` — wired in
  `wireCallbacks` to a closure that calls `client.MarkChannel` +
  `db.UpdateLastReadTS` + `lastReadMap[channelID] = ts`, returning
  `ChannelMarkedReadMsg`. Mirrors the side-effect logic currently
  embedded in `channelFetcher` (`cmd/slk/main.go:663-673`); pulling it
  out lets Tier 1 mark-as-read without firing a `GetHistory`. The
  existing `channelFetcher` keeps calling the same logic inline for
  Tier 2/3 to preserve today's behavior unchanged.

#### Write paths

- `fetchChannelMessages` calls `db.SetChannelSyncedAt(channelID,
  time.Now().Unix())` immediately after the authoritative-replace
  upsert loop completes successfully. Failed fetches (nil return) do
  not bump `synced_at`.
- `rtmEventHandler.OnMessage` calls `db.SetChannelSyncedAt(channelID,
  time.Now().Unix())` after upserting (regardless of active workspace).
  A live WS feed is at least as authoritative as a `GetHistory`
  snapshot, so a recently-WS-active channel skips redundant fetches.

#### MessagesLoadedMsg

Clears the syncing indicator and (implicitly via fetchChannelMessages
above) leaves `synced_at` already updated:

```go
case MessagesLoadedMsg:
    if msg.ChannelID == a.activeChannelID {
        a.statusbar.SetSyncing(false)
        // existing logic
    }
```

### 4. Status-bar sync indicator

Additions to `internal/ui/statusbar/model.go`:

```go
type Model struct {
    ...
    syncing bool
}

func (m *Model) SetSyncing(syncing bool) {
    if m.syncing == syncing { return }
    m.syncing = syncing
    m.dirty()
}
```

`View()` conditionally renders a static `○` glyph next to the channel
name, styled with a new themable entry (`styles.StatusbarSyncing`).

App wiring:

- Tier 2 path in `ChannelSelectedMsg`: `SetSyncing(true)`.
- `MessagesLoadedMsg` (active channel): `SetSyncing(false)`.
- `WorkspaceSwitchedMsg`: `SetSyncing(false)` defensively.

### 5. WorkspaceSwitchedMsg cleanup

Remove lines 2000-2004 of `internal/ui/app.go` (the
`SetLoading(true)` + `SetMessages(nil)` + `SpinnerTickMsg` block).
Replace with:

```go
if len(msg.Channels) == 0 {
    // Empty workspace - no queued ChannelSelectedMsg will repaint;
    // we must clear the pane ourselves.
    a.messagepane.SetLoading(false)
    a.messagepane.SetMessages(nil)
}
// Non-empty: the queued ChannelSelectedMsg below repaints (Tier 1/2/3).
```

### 6. Async user resolution

#### New helper

```go
// cmd/slk/main.go
func resolveUserCached(userID string, userNames map[string]string, db *cache.DB) (string, bool) {
    if name, ok := userNames[userID]; ok && name != "" {
        return name, true
    }
    if u, err := db.GetUser(userID); err == nil {
        name := u.DisplayName
        if name == "" { name = u.Name }
        if name != "" {
            userNames[userID] = name
            return name, true
        }
    }
    return "", false
}
```

#### Workspace resolver

A `*userResolver` field on `WorkspaceContext`, constructed in
`connectWorkspace`:

```go
type userResolver struct {
    teamID    string
    client    *slackclient.Client  // this workspace's client; user IDs are workspace-scoped
    db        *cache.DB
    avatars   *avatar.Cache
    userNames map[string]string    // shared with WorkspaceContext.UserNames
    send      func(tea.Msg)        // p.Send
    inflight  sync.Map             // userID -> struct{}
}

func (r *userResolver) Request(userID string) {
    if userID == "" { return }
    if _, exists := r.inflight.LoadOrStore(userID, struct{}{}); exists {
        return
    }
    go func() {
        defer r.inflight.Delete(userID)
        u, err := r.client.GetUserProfile(userID)
        if err != nil { return }
        name := u.Profile.DisplayName
        if name == "" { name = u.RealName }
        if name == "" { name = u.Name }
        isBot := u.IsBot || u.IsAppUser
        r.userNames[userID] = name
        r.avatars.Preload(userID, u.Profile.Image32)
        _ = r.db.UpsertUser(cache.User{
            ID: userID, WorkspaceID: r.teamID,
            Name: u.Name, DisplayName: name,
            AvatarURL: u.Profile.Image32,
            Presence: "away", IsBot: isBot,
        })
        r.send(ui.UserResolvedMsg{
            TeamID: r.teamID, UserID: userID,
            DisplayName: name, IsBot: isBot,
        })
    }()
}
```

#### Call-site changes

In `fetchChannelMessages`, `fetchOlderMessages`, `fetchThreadReplies`,
`enrichCachedRow`, and `rtmEventHandler.OnMessage`, replace the inline
`resolveUser` call with:

```go
userName, ok := resolveUserCached(m.User, userNames, db)
if !ok {
    userName = m.User  // fallback shown until UserResolvedMsg lands
    wctx.UserResolver.Request(m.User)
}
```

The synchronous "known name but no avatar → fetch profile for avatar"
branch in today's `resolveUser` (lines 1461-1477) is dropped. The
avatar prefetcher (`imgrender`) already lazy-loads avatars on render.

#### New message type and handler

```go
// internal/ui/app.go
UserResolvedMsg struct {
    TeamID      string
    UserID      string
    DisplayName string
    IsBot       bool
}

case UserResolvedMsg:
    if msg.TeamID != a.activeTeamID { break }
    a.messagepane.PatchUserName(msg.UserID, msg.DisplayName)
    a.threadPanel.PatchUserName(msg.UserID, msg.DisplayName)
    // Existing DM-classification logic for IsBot (mirrors DMNameResolvedMsg path)
    // applies only when this user is the peer of a DM; out of scope here.
```

#### Messages model patch method

```go
// internal/ui/messages/model.go
func (m *Model) PatchUserName(userID, displayName string) {
    if m.userNames == nil { m.userNames = map[string]string{} }
    if m.userNames[userID] == displayName { return }
    m.userNames[userID] = displayName
    changed := false
    for i := range m.messages {
        if m.messages[i].UserID == userID && m.messages[i].UserName != displayName {
            m.messages[i].UserName = displayName
            changed = true
        }
    }
    if changed {
        m.cache = nil
        m.dirty()
    }
}
```

`internal/ui/thread/model.go` gets the same method for thread replies.

## Data flow on workspace switch (after fix)

```
User presses Ctrl+2
  -> SwitchWorkspaceFunc(teamID=B)
       router.Set(router.ByID(B))    ← single atomic store
       returns WorkspaceSwitchedMsg
  -> App handler:
       state resets (sidebar, threads, theme, etc.)
       restores lastChannelByTeam[B] = C
       queues ChannelSelectedMsg{C}
       SetSyncing(false)
       (NO SetMessages(nil), NO spinner-tick)
  -> ChannelSelectedMsg{C}:
       syncedAt := channelSyncedAtReader(C)
       age := now - syncedAt
       if age < 30s: SetMessages(cached); done.
       if age < 5m:  SetMessages(cached); SetSyncing(true); fire fetch.
       else:         SetLoading(true); SetMessages(nil); fire fetch + spinner.
  -> (Tier 2 path) channelFetcher closure reads router.Active() => B's client
                   fetchChannelMessages calls Slack
                   for each unknown author: enqueue resolver.Request(uid)
                   returns MessagesLoadedMsg
  -> MessagesLoadedMsg:
       SetMessages(fresh)
       SetSyncing(false)
       (synced_at already bumped inside fetchChannelMessages)
  -> UserResolvedMsg (one per resolved author, async):
       PatchUserName(uid, name)
       messagepane re-renders that row's username
```

## Error handling

- `router.Active() == nil`: defensive guard at the top of every callback.
  Returns nil msg. Can only happen during the narrow window between
  program start and the first `router.Set`.
- `wctx.UserResolver.Request` for unknown user: dedup'd by `sync.Map`,
  failures silently drop (existing behavior — the user just stays
  rendered as their ID).
- `db.GetChannelSyncedAt` error: treat as `synced_at = 0` → Tier 3.
- `SetChannelSyncedAt` write failure: logged via `debuglog.Cache`,
  doesn't fail the fetch. Cache will be re-fetched on next select.
- WS-driven `synced_at` bump in `OnMessage`: best-effort, logged only.

## Testing

### Unit tests

- `internal/cache/channels_test.go`: schema migration applies cleanly
  to an existing pre-migration DB; `GetChannelSyncedAt` returns 0 for
  unknown channels and the last-written value for known ones.
- `internal/ui/messages/model_test.go`: `PatchUserName` patches all
  matching rows, invalidates the render cache (`Version()` increments),
  is a no-op when name is unchanged.
- `internal/ui/thread/model_test.go`: same `PatchUserName` coverage.
- `internal/ui/statusbar/model_test.go`: `SetSyncing(true)` renders the
  glyph; `SetSyncing(false)` removes it; idempotent on no-change.
- `internal/ui/app_test.go`:
  - Bootstrap race fixture: send two `WorkspaceReadyMsg` in one tick,
    one with `InitialActive: true`, the other with `false`; only the
    `true` one queues `ChannelSelectedMsg`. A second `WorkspaceReadyMsg`
    with `InitialActive: true` (defensive — shouldn't happen) is a no-op
    due to `bootstrapActiveClaimed`.
  - Three-tier `ChannelSelectedMsg`: stub the `channelSyncedAtReader`
    to return values at the boundaries (0, 29 s ago, 31 s ago, 4 m
    59 s ago, 5 m 1 s ago); assert the right combination of
    `SetMessages`, `SetLoading`, `SetSyncing`, queued `channelFetcher`
    cmd, and queued `channelReadMarker` cmd. Tier 1 with empty cache
    fires no marker (nothing to mark).
  - `WorkspaceSwitchedMsg` no longer wipes pane (existing assertions
    on intermediate state will need updating).
  - `UserResolvedMsg` for wrong workspace drops; for active workspace
    patches both `messagepane` and `threadPanel`.

### Integration / smoke

- Multi-workspace startup against mocked Slack: assert that
  `wireCallbacks` is called exactly once and that only the configured
  default workspace auto-selects a channel.
- End-to-end channel switch within a workspace using a fake clock:
  Tier 1 path renders cache instantly and fires no fetch; subsequent
  switch after >30 s advances to Tier 2.

### Manual reproduction

Re-run the original repro (Truelist ⇄ Rands with stale cache).
Expected new behavior:
1. Open slk. Cache-first paint for the default workspace's first channel.
2. Switch to other workspace via Ctrl-2. No `channel_not_found` error
   in `slk-debug.log`. Cache-first paint with `○` indicator.
3. Within ~300 ms the indicator disappears and the pane reflects fresh
   data. No visible "older then newer" jump.
4. Switch back. `synced_at` was just bumped, so Tier 1 fires: instant
   cache render, no fetch, no indicator.

### Smoke-test checklist (evaluate after Plan B implementation lands)

Once the changes are running end-to-end, drive these scenarios and
revisit the calibration decisions:

- **Tier thresholds.** 30 s / 5 min are round-number guesses.
  - Does Tier 1 (no fetch) feel correct for typical back-to-back
    revisits? Try the j/k channel-finder cycle on a workspace with
    20+ channels and confirm the lack of network noise feels right,
    not stale.
  - Does Tier 3 (spinner-only) kick in too aggressively? Sit on a
    workspace > 5 min and re-enter a channel; if the spinner feels
    jarring for what's actually still-fresh data (no new WS messages
    since you left it), consider raising to 15 min or making the
    threshold depend on whether the WS has received any messages
    for the channel since the last sync.
- **`channelReadMarker` vs always-fire-fetcher.** With async user
  resolution making fetches ~300 ms, the perf win of Tier 1 skipping
  GetHistory is small. If `channelReadMarker` adds friction in code
  review or testing, fold it back and have Tier 1 always fire the
  fetcher (which already does mark-as-read). Decide based on real
  fetch latency observed in `slk-debug.log` post-merge.
- **Indicator visibility timing.** The `○` glyph appears immediately on
  Tier 2 entry. If the fetch consistently returns under ~100 ms (cached
  users on a small workspace), the indicator may flash too briefly to
  read. Consider a delay-before-show (e.g. only show if fetch hasn't
  returned within 150 ms).
- **`userResolver` parallelism.** Watch `slk-debug.log` for
  `users.info` calls during the first channel-select on a busy
  workspace. If you see Slack rate-limit responses (HTTP 429) or
  unusually slow profile fetches due to contention, add a per-resolver
  buffered semaphore (~8 concurrent).
- **Reconnect freshness.** Disconnect (e.g. toggle wifi), wait > 5 min,
  reconnect. Confirm the first channel-select after reconnect falls
  into Tier 3 (spinner-only) due to `synced_at` aging out. If it
  doesn't, add explicit `synced_at = 0` clearing for the workspace's
  channels inside `OnConnect`.
- **`OnMessage`-driven `synced_at` bumps.** Verify via debug log that
  WS messages for the active workspace bump `synced_at`, and that a
  subsequent channel revisit therefore correctly takes the Tier 1
  fast-path. If the bump isn't happening reliably, the cache-fresh
  optimization silently degrades to Tier 2.

## File-by-file summary

| File | Changes |
|---|---|
| `internal/cache/db.go` | Schema migration adds `channels.synced_at`. |
| `internal/cache/channels.go` | `SetChannelSyncedAt`, `GetChannelSyncedAt`. |
| `internal/cache/channels_test.go` | Migration + getter/setter tests. |
| `internal/ui/app.go` | `bootstrapActiveClaimed` field, `WorkspaceReadyMsg.InitialActive` field, `SetChannelSyncedAtReader`, `SetChannelReadMarker`, three-tier `ChannelSelectedMsg`, `UserResolvedMsg` and handler, syncing flag plumbing, `WorkspaceSwitchedMsg` wipe removed. |
| `internal/ui/app_test.go` | New tests (InitialActive claim race, three tiers, UserResolved); update existing tests that asserted on intermediate `WorkspaceSwitchedMsg` state. |
| `internal/ui/messages/model.go` | `PatchUserName`. |
| `internal/ui/messages/model_test.go` | `PatchUserName` tests. |
| `internal/ui/thread/model.go` | `PatchUserName` (mirror). |
| `internal/ui/thread/model_test.go` | `PatchUserName` tests. |
| `internal/ui/statusbar/model.go` | `syncing` field, `SetSyncing`, View conditional. |
| `internal/ui/statusbar/model_test.go` | `SetSyncing` tests. |
| `internal/ui/styles/...` | `StatusbarSyncing` themable entry. |
| `cmd/slk/main.go` | `workspaceRouter`, single `wireCallbacks(router)` call before `p.Run`, `userResolver` per workspace bound to `wctx.Client`, `resolveUserCached`, async resolver request in five call sites, `firstReady sync.Once` setting `InitialActive` on each `WorkspaceReadyMsg`, mark-as-read closure split into a `channelReadMarker` callback alongside `channelFetcher`. |
| `cmd/slk/main_test.go` (if exists) | Coverage for `workspaceRouter` swap + active reads. |

## Open questions

- **Bounded concurrency for `userResolver`?** Today's design fires
  one goroutine per unknown user with `sync.Map`-based dedup. On a
  workspace with 50 unique unknown authors in one history fetch,
  that's 50 parallel `users.info` calls. If Slack rate-limits or
  the user reports new errors in `slk-debug.log`, wrap requests in
  a per-workspace buffered semaphore (e.g. 8 concurrent). Out of
  scope for v1; revisit if symptoms emerge.
- **Reconnection invalidation?** When a workspace WS reconnects after
  a long disconnect, we may want to bump-clear all channels' `synced_at`
  to force re-fetch. Today's design relies on the 5-minute stale
  threshold to do this naturally. If users report stale data after
  reconnect, add explicit clearing in `OnConnect`.
