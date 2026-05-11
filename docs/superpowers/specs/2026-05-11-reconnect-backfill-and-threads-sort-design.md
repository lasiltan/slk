# Reconnect backfill and threads-view sort

## Problem

Two bugs in the workspace-wide threads view, both observable with a
recently-copied production `cache.db`.

### 1. Messages posted during sleep/disconnect never reach the cache

The user reports an Emerson @-mention in `#engineering` is invisible in
slk. The message is ~1 hour old; the user's laptop was asleep around
the time it was posted, then woke up. The local cache has 54 messages
for `#engineering` (`C050WUX3W95`) but the newest is from ~9 hours ago.

The mechanism:

1. The laptop sleeps. slk's WebSocket read goroutine in
   `internal/slack/client.go:242-258` hits its 60 s read deadline,
   the read errors, the goroutine exits, and the handler's
   `OnDisconnect` fires.
2. The laptop wakes. slk re-establishes the WebSocket. The handler's
   `OnConnect` runs (`cmd/slk/main.go:2664-2688`). It refreshes
   presence, DND, and Slack-native section state. **It does not
   backfill any messages.**
3. Messages posted while the WS was dead were never delivered to slk
   and are not refetched. They are only retrieved if the user later
   opens the affected channel, which calls `fetchChannelMessages` →
   `conversations.history` via the existing channel-select flow.

For the user's case, `#engineering` has not been opened during the
current session, so the missed message stays missing.

Side effects:

- The threads view (`internal/cache/threads.go:ListInvolvedThreads`)
  cannot show threads it has no cached evidence of involvement in.
  The Emerson thread parent contains `<@U05AZM7KJ1H>` (the user's
  mention); without the parent in the cache, the involvement filter
  has nothing to match.
- Any reply by a third party in an existing thread also stays
  invisible — including thread replies in channels with cached
  history.

### 2. Threads-view sort splits the list into a false-positive-unread block and a "read" block

Screenshot of the user's UE threads view shows the Jayana thread (last
reply by the user, 1 hour ago) at position 9, below an 8-row block of
threads whose newest reply is 1 h old and oldest is 19 d old. The
order within each block is `LastReplyTS` DESC; the boundary between
the blocks is whether the user replied last.

The mechanism is in `internal/cache/threads.go`:

```go
// line 95
s.Unread = s.LastReplyTS > lastRead && s.LastReplyBy != selfUserID

// lines 102-108
sort.SliceStable(out, func(i, j int) bool {
    if out[i].Unread != out[j].Unread {
        return out[i].Unread
    }
    return out[i].LastReplyTS > out[j].LastReplyTS
})
```

`lastRead` is the channel's `last_read_ts` column. For the user's UE
workspace, 445 of 446 channels have `last_read_ts = ''`. Any non-empty
`LastReplyTS` compares as `> ''` (SQLite string comparison), so the
Unread predicate degrades to `LastReplyBy != selfUserID`. Every thread
where someone else replied last is flagged Unread and sorted ahead of
every thread where the user replied last, regardless of when those
replies actually happened.

The empty `last_read_ts` for nearly every channel is itself a
separate cache-integrity bug (it is populated for 63/161 channels in
the user's Rands workspace, so the code path works; UE's channel
bootstrap is producing empty values). This spec does **not** fix that
upstream bug.

## Goals

1. After WebSocket reconnect, every channel that already has cached
   messages catches up on activity it missed during the disconnect,
   including thread replies in threads the user is involved in.

2. The threads view orders rows by recency of last reply, full stop.
   The visual unread indicator stays; it just doesn't move rows
   around.

## Non-goals

- Fixing `is_member=0` for every UE channel (separate cache-integrity
  bug).
- Fixing `last_read_ts=''` for most UE channels (separate
  cache-integrity bug; reduces the impact of #2 but doesn't cause
  the sort issue we're fixing).
- Switching the threads view from the local `user_id = self OR text
  LIKE '<@self>'` heuristic to Slack's `subscriptions.thread` API
  (v2 plan called out in `cache/threads.go:30-32`).
- Removing `lazy_channels=1` from the flannel WebSocket URL.
  Reconnect backfill substantially reduces its impact for the common
  sleep-recovery case.
- Backfilling channels that have zero cached messages (DMs and
  channels the user has never visited in slk).

## Design

### Part 1: Reconnect backfill

#### Trigger

Add a new step inside `rtmEventHandler.OnConnect` in
`cmd/slk/main.go:2664-2688`, after the existing presence/DND refresh
and the section rebootstrap. Reuse the same `30 s` dedupe pattern
that `SectionStore.MaybeRebootstrap` already uses so a rapid
disconnect flap (network blip every few seconds) doesn't thunder.

Concretely: track a per-handler `lastBackfillAt time.Time`. If less
than `30 s` since the last run, skip. Otherwise, set `lastBackfillAt
= time.Now()` and dispatch the backfill goroutine.

The dedupe is per-`rtmEventHandler`, which is per-workspace.

Backfill runs in a goroutine off the WS read goroutine — same shape
as the existing `bootstrapPresenceAndDND` call at line 2668. The WS
read goroutine must not block on HTTP work.

#### Channel selection

The cache currently has no view that returns "channels with cached
messages". Add one:

```go
// internal/cache/channels.go (new function)
func (db *DB) ChannelsWithMessages(workspaceID string) ([]ChannelSyncRow, error)
```

Returns a slice of `{ChannelID, SyncedAt}` for every distinct
`channel_id` in the `messages` table for the given workspace,
together with `channels.synced_at`. SQL sketch:

```sql
SELECT DISTINCT m.channel_id, COALESCE(c.synced_at, 0) AS synced_at
FROM messages m
LEFT JOIN channels c ON c.id = m.channel_id
WHERE m.workspace_id = ?
```

For the user's UE cache that's 37 rows.

Place the `ChannelSyncRow` type next to existing channel types in
`internal/cache/channels.go`. Add a unit test covering empty
workspace, one channel one message, two channels with synced_at
populated, and a channel in another workspace correctly excluded.

#### History fetch per channel

For each `(channelID, syncedAt)` returned, dispatch to a worker pool
with `concurrency=4`. Each worker runs:

```go
oldest := strconv.FormatInt(syncedAt, 10) + ".000000"  // syncedAt is unix seconds; convert to Slack TS
if syncedAt == 0 {
    oldest = "" // no prior sync — fetch most recent page only
}
hist, err := wctx.Client.GetHistorySince(ctx, channelID, oldest, /*limit=*/200)
```

`GetHistorySince` is a new wrapper added to `internal/slack/client.go`
alongside the existing `GetOlderHistory`. It calls
`conversations.history` with `oldest=oldest` and paginates forward
using `has_more`/`response_metadata.next_cursor` until exhausted, or
until a hard cap (proposal: 500 messages per channel per reconnect)
is reached. The cap protects against runaway backfills after very
long sleeps in busy channels. If the cap is hit, log a warning and
stop; the next user-driven channel-open will fetch the rest.

Handle `RateLimitedError` the same way `GetChannels` does
(`internal/slack/client.go:319-332`): sleep `RetryAfter` (default 30 s)
and retry the same page. The 4-wide concurrency keeps us under
Tier 3's 50/min limit comfortably.

For each returned message:

- Synthesize a `slack.Message` with the returned fields (mirroring
  the existing `OnMessage` upsert at `cmd/slk/main.go:2440-2466`).
- `db.UpsertMessage` (PK `(ts, channel_id)` so it's idempotent and
  safe to re-run).
- Collect `(channelID, thread_ts)` into a per-channel set whenever
  `thread_ts != ""`.

After the channel completes, call `db.SetChannelSyncedAt(channelID,
time.Now().Unix())` once.

#### Thread-replies backfill

After every channel has been processed (or as each channel
completes — design choice deferred to implementation; either works
correctness-wise), take the union of all collected `(channel_id,
thread_ts)` pairs across the workspace.

Filter to threads where the user is involved using cache state, not
a network call. Reuse the same predicate `cache/threads.go`
already implements:

```sql
SELECT 1 FROM messages
WHERE workspace_id = ? AND channel_id = ? AND thread_ts = ?
  AND is_deleted = 0
  AND (user_id = ? OR text LIKE ?)
LIMIT 1
```

Add a helper `db.ThreadInvolvesUser(workspaceID, channelID,
threadTS, selfUserID string) (bool, error)` next to
`ListInvolvedThreads`.

For each surviving thread, call the existing `fetchThreadReplies`
(`cmd/slk/main.go:2221-2308`) which already runs
`conversations.replies`, upserts every returned message, and bumps
synced_at. Run these through the same 4-wide pool used for channels
(or a second pool — implementation detail).

#### After backfill: refresh the threads view

Once every channel-fetch and thread-fetch goroutine has finished for
a workspace, send a single `ui.ThreadsListDirtyMsg{TeamID:
h.workspaceID}` via `h.program.Send`. The existing handler in
`internal/ui/app.go:1950-1955` debounces dirty messages and triggers
a re-query of `ListInvolvedThreads`. The result is gated by
`msg.TeamID == a.activeTeamID`, so backfill of an inactive workspace
correctly does not redraw the active view.

If the active workspace is the one being backfilled and the user is
currently viewing the threads view, they see the list rebuild with
the newly-cached threads.

#### Logging

Add a new `[backfill]` category. Emit lines at the boundaries:

```
[backfill] team=TUJLNE62Z trigger=reconnect channels=37 start
[backfill] team=TUJLNE62Z channel=C050WUX3W95 oldest=1778504963.000000 count=12 dur_ms=380
[backfill] team=TUJLNE62Z channel=C050WUX3W95 capped_at=500 reason=cap
[backfill] team=TUJLNE62Z channel-phase done total_msgs=143 dur_ms=2100
[backfill] team=TUJLNE62Z thread-phase threads_involved=4 done dur_ms=900
[backfill] team=TUJLNE62Z trigger=reconnect total_dur_ms=3050 status=ok
```

These let the user grep `[backfill]` to verify the path runs when
they expect it to.

#### Failure handling

Each HTTP call has its own error handling; one channel's failure
does not abort the workspace's backfill. Failures log a warning and
move on. The next reconnect (or the user opening the channel) gets
another chance.

#### Edge case: workspace not yet ready

`OnConnect` can fire before the workspace's initial bootstrap has
finished (e.g., during the connect-goroutine race documented in the
recent `messages-cache-integrity` work). Gate the backfill on
`wctx != nil && wctx.firstReady` (an existing `sync.Once`-guarded
flag set after `WorkspaceReadyMsg`). If the workspace isn't ready,
skip — the initial bootstrap covers the same ground.

### Part 2: Threads view sort

Single change in `internal/cache/threads.go:102-108`:

```go
// Order: newest LastReplyTS first.
sort.SliceStable(out, func(i, j int) bool {
    return out[i].LastReplyTS > out[j].LastReplyTS
})
```

The `s.Unread` field stays. Render-side (`internal/ui/threadsview/
model.go:531-533`) still draws the `●` dot for `s.Unread == true`,
and `UnreadCount` still drives the sidebar badge.

Update `internal/cache/threads_test.go`:

- Rename `TestListInvolvedThreads_OrderingUnreadFirst` to
  `TestListInvolvedThreads_OrderingByLastReplyTS`.
- Adjust the expected slice order to be purely descending by
  `LastReplyTS`, independent of `Unread`.
- Add a case asserting that a thread with `Unread=true` and an older
  `LastReplyTS` sorts *after* a thread with `Unread=false` and a
  newer `LastReplyTS` — the exact case that's currently inverted in
  the user's screenshot.

No changes to `threadsview` package, no changes to the unread-badge
counter on the sidebar.

## Data flow after both fixes

```
WS disconnect (laptop sleeps)
  ↓
WS reconnect (laptop wakes) → handler.OnConnect
  ↓
   ├── presence/DND refresh (existing)
   ├── section state rebootstrap (existing)
   └── reconnect backfill (NEW)
        ↓
        Channels with cached messages → conversations.history(oldest=synced_at), pool=4
          ↓
          UpsertMessage per result; collect thread_ts set
        ↓
        Threads involving the user → fetchThreadReplies, pool=4
          ↓
          UpsertMessage per reply
        ↓
        ThreadsListDirtyMsg{TeamID}
          ↓
          app.go re-fetches → ListInvolvedThreads
            ↓
            Sort by LastReplyTS DESC (NEW)
            ↓
            threadsview.SetSummaries → render
```

## Testing

### Unit tests

- `cache/channels_test.go`: `TestChannelsWithMessages_*` covering
  empty workspace, single-channel single-message, multi-channel,
  cross-workspace isolation, and synced_at propagation.
- `cache/threads_test.go`: updated `TestListInvolvedThreads_
  OrderingByLastReplyTS`. New
  `TestListInvolvedThreads_UnreadDoesNotChangeOrder` asserting
  the specific Unread=true-older vs Unread=false-newer inversion
  that the user reported.
- `cache/threads_test.go`: new `TestThreadInvolvesUser_*` for the
  helper used by the thread-replies filter.

### Integration test

Add `cmd/slk/backfill_test.go` (or co-locate next to existing
fetch-related tests). Stub the slack client with a fake that
returns canned `conversations.history` results. Verify:

- A reconnect with one channel-with-messages triggers one history
  call with the right `oldest` parameter.
- Returned messages are upserted and `synced_at` is bumped.
- A thread_ts in the results triggers a `conversations.replies`
  call iff the user is involved (per the predicate).
- The pool concurrency cap is respected (drive with 8 channels and
  a counted-concurrent fake client; assert ≤4 in flight).
- The 30-second dedupe blocks a second reconnect within the
  window.
- The 500-message-per-channel cap stops paginating at the cap.

### Manual verification on the user's environment

After the change, with the same /tmp/cache.db setup:

1. Start slk on the grant-work user. Verify the threads view shows
   the Jayana thread near position 2 (not 9).
2. From another Slack client, post in `#engineering`. Don't
   open `#engineering` in slk. Lock the screen / sleep the laptop
   for 90 seconds. Wake up. Verify within ~5 seconds that the new
   message appears in the threads view (if you were @-mentioned)
   or that opening `#engineering` shows it instantly from cache.
3. Tail `slk-debug.log` and grep `[backfill]`. Verify a backfill
   pass ran on reconnect.

## Open questions

None blocking. Implementation may choose between fetching thread
replies per-channel-completion vs after-all-channels-done; either is
correct.
