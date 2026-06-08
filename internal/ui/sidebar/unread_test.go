package sidebar

import (
	"strings"
	"testing"

	"github.com/gammons/slk/internal/cache"
)

// withReader installs a static read-state map for tests.
func withReader(m *Model, state map[string]cache.ReadState) {
	m.SetReadStateReader(func() map[string]cache.ReadState {
		return state
	})
}

// TestUnreadSection_AppearsBetweenCustomsAndDefaults verifies that the
// "Unread" section sits AFTER any user-defined custom sections and
// BEFORE the default fallback sections when at least one uncategorized
// channel is visibly unread.
func TestUnreadSection_AppearsBetweenCustomsAndDefaults(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
		{ID: "C2", Name: "deploys", Type: "channel", Section: "Eng", SectionOrder: 1},
		{ID: "D1", Name: "alice", Type: "dm"},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{"C1": {HasUnread: true}})
	// SetReadStateReader doesn't rebuild filter/nav; recompute ordering
	// against the live reader by simulating a no-op refresh.
	m.SetItems(items)

	got := m.modelOrderedSections(m.filtered)
	want := []string{"Eng", defaultUnreadSection, defaultDMSection, defaultChannelsSection}
	if !equalSlice(got, want) {
		t.Fatalf("section order: got %v, want %v", got, want)
	}
}

// TestUnreadSection_AbsentWhenNoUnreads verifies that no Unread section
// is emitted when every uncategorized channel is read.
func TestUnreadSection_AbsentWhenNoUnreads(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
		{ID: "D1", Name: "alice", Type: "dm"},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{})
	m.SetItems(items)

	got := m.modelOrderedSections(m.filtered)
	for _, name := range got {
		if name == defaultUnreadSection {
			t.Fatalf("Unread should be absent; got %v", got)
		}
	}
}

// TestUnreadSection_ExcludesCustomSectionItems verifies that an unread
// channel which belongs to a user-defined custom section is NOT pulled
// into Unread. The Unread section is only for *uncategorized* unreads.
func TestUnreadSection_ExcludesCustomSectionItems(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "deploys", Type: "channel", Section: "Eng", SectionOrder: 1},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{"C1": {HasUnread: true}})
	m.SetItems(items)

	got := m.modelOrderedSections(m.filtered)
	for _, name := range got {
		if name == defaultUnreadSection {
			t.Fatalf("Unread must not appear when the only unread is in a custom section; got %v", got)
		}
	}
}

// TestUnreadSection_DuplicatesRowInNav verifies that a qualifying unread
// channel appears as TWO distinct navChannel rows -- one under Unread,
// one under the default fallback section -- both pointing at the same
// underlying ChannelItem.
func TestUnreadSection_DuplicatesRowInNav(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{"C1": {HasUnread: true}})
	// Channels starts collapsed by default; expand so its copy is in nav.
	m.ToggleCollapse(defaultChannelsSection)
	m.SetItems(items) // force a nav rebuild against the reader

	count := 0
	sections := map[string]bool{}
	for _, n := range m.nav {
		if n.kind != navChannel {
			continue
		}
		if m.items[m.filtered[n.fi]].ID != "C1" {
			continue
		}
		count++
		sections[n.section] = true
	}
	if count != 2 {
		t.Fatalf("expected C1 to appear in nav twice, got %d (sections=%v)", count, sections)
	}
	if !sections[defaultUnreadSection] || !sections[defaultChannelsSection] {
		t.Fatalf("expected C1 under both Unread and Channels, got sections=%v", sections)
	}
}

// TestUnreadSection_MarkReadDropsSectionAndPreservesCursor verifies the
// mark-as-read flow: when the only Unread item becomes read, the
// section disappears entirely, and a cursor sitting on the Unread copy
// of that channel falls back to the channel's default-section copy via
// the rebuildNavPreserveCursor ID-only fallback path.
func TestUnreadSection_MarkReadDropsSectionAndPreservesCursor(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
	}
	m := New(items)
	state := map[string]cache.ReadState{"C1": {HasUnread: true}}
	m.SetReadStateReader(func() map[string]cache.ReadState { return state })
	m.ToggleCollapse(defaultChannelsSection) // expand Channels too
	m.SetItems(items)

	// Park cursor on the Unread copy of C1.
	target := -1
	for i, n := range m.nav {
		if n.kind == navChannel && n.section == defaultUnreadSection {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("expected an Unread copy of C1 in nav; nav=%+v", m.nav)
	}
	m.cursor = target
	if id := m.SelectedID(); id != "C1" {
		t.Fatalf("precondition: cursor should select C1, got %q", id)
	}

	// Mark read -- mutate the shared state map (the reader closes over it)
	// and trigger a refresh.
	state["C1"] = cache.ReadState{HasUnread: false}
	m.Invalidate()
	m.SetItems(items) // rebuilds filter + nav with new read state

	// Section header should be gone.
	for _, n := range m.nav {
		if n.kind == navHeader && n.header == defaultUnreadSection {
			t.Fatalf("Unread header should be gone after mark-as-read; nav=%+v", m.nav)
		}
	}
	// Cursor should still resolve to C1 (now in the Channels copy).
	if id := m.SelectedID(); id != "C1" {
		t.Fatalf("cursor should fall back to the Channels copy of C1; got %q", id)
	}
}

// TestUnreadSection_CollapseTogglePreservesCursor verifies that
// collapsing then re-expanding the Unread header preserves the cursor's
// section identity. After expand, cursor lands on the header again
// (matching the existing collapse-toggle semantics for other sections).
func TestUnreadSection_CollapseTogglePreservesCursor(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{"C1": {HasUnread: true}})
	m.SetItems(items)

	// Park cursor on the Unread header.
	hdr := -1
	for i, n := range m.nav {
		if n.kind == navHeader && n.header == defaultUnreadSection {
			hdr = i
			break
		}
	}
	if hdr < 0 {
		t.Fatalf("expected Unread header in nav; nav=%+v", m.nav)
	}
	m.cursor = hdr

	m.ToggleCollapse(defaultUnreadSection)
	if name, ok := m.IsSectionHeaderSelected(); !ok || name != defaultUnreadSection {
		t.Fatalf("after collapse: cursor should stay on Unread header, got name=%q ok=%v", name, ok)
	}
	m.ToggleCollapse(defaultUnreadSection)
	if name, ok := m.IsSectionHeaderSelected(); !ok || name != defaultUnreadSection {
		t.Fatalf("after re-expand: cursor should stay on Unread header, got name=%q ok=%v", name, ok)
	}
}

// TestUnreadSection_AggregateBadgeMatchesCount verifies the aggregate
// badge value on a collapsed Unread header equals the number of
// uncategorized + visibly-unread channels.
func TestUnreadSection_AggregateBadgeMatchesCount(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
		{ID: "C2", Name: "random", Type: "channel"},
		{ID: "C3", Name: "ops", Type: "channel"},
		{ID: "C4", Name: "eng", Type: "channel", Section: "Eng", SectionOrder: 1},
	}
	m := New(items)
	// C1 + C2 are uncategorized unreads (count toward Unread); C3 is
	// read; C4 is unread BUT in a custom section so it must NOT count.
	withReader(&m, map[string]cache.ReadState{
		"C1": {HasUnread: true},
		"C2": {HasUnread: true},
		"C4": {HasUnread: true},
	})
	m.SetItems(items)

	got := m.aggregateUnreadForSection(defaultUnreadSection)
	if got != 2 {
		t.Fatalf("Unread aggregate count: got %d, want 2", got)
	}
}

// TestUnreadSection_BadgeRendersOnCollapsedHeader is an end-to-end check
// that the "•2" badge string actually shows up in the rendered View
// output when Unread is collapsed with two qualifying items.
func TestUnreadSection_BadgeRendersOnCollapsedHeader(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
		{ID: "C2", Name: "random", Type: "channel"},
	}
	m := New(items)
	withReader(&m, map[string]cache.ReadState{
		"C1": {HasUnread: true},
		"C2": {HasUnread: true},
	})
	m.SetItems(items)
	m.ToggleCollapse(defaultUnreadSection)

	out := m.View(20, 30)
	if !strings.Contains(out, "•2") {
		t.Fatalf("expected '•2' badge on collapsed Unread header; output:\n%s", out)
	}
}

// TestUnreadSection_AppearsAtRuntimeIsNavigable verifies that when a
// channel becomes unread AFTER initial sidebar build (the realistic
// runtime path: ReadStateChangedMsg -> sidebar.Invalidate), the new
// Unread section header AND its channel row become navigable via
// cursor movement. Regression test: Invalidate originally only
// flipped cacheValid, leaving m.nav stale; buildCache would render
// the Unread section header from the fresh sectionOrder but the new
// channel rows got navIdx=-1 (no entry in stale m.nav), so j/k
// silently skipped over them.
func TestUnreadSection_AppearsAtRuntimeIsNavigable(t *testing.T) {
	items := []ChannelItem{
		{ID: "C1", Name: "general", Type: "channel"},
	}
	m := New(items)
	// Start with no unreads. Unread section should not exist.
	state := map[string]cache.ReadState{}
	m.SetReadStateReader(func() map[string]cache.ReadState { return state })
	m.SetItems(items)
	for _, n := range m.nav {
		if n.kind == navHeader && n.header == defaultUnreadSection {
			t.Fatalf("precondition: Unread header must not exist before unread arrives; nav=%+v", m.nav)
		}
	}

	// Simulate a new unread arriving: mutate read state, fire only
	// Invalidate (NOT SetItems — that would mask the bug by also
	// rebuilding nav).
	state["C1"] = cache.ReadState{HasUnread: true}
	m.Invalidate()

	// Unread header must now be in nav AND there must be a navChannel
	// entry for C1 under the Unread section.
	var hdr, ch int = -1, -1
	for i, n := range m.nav {
		switch {
		case n.kind == navHeader && n.header == defaultUnreadSection:
			hdr = i
		case n.kind == navChannel && n.section == defaultUnreadSection:
			ch = i
		}
	}
	if hdr < 0 {
		t.Fatalf("Unread header missing from nav after runtime unread; nav=%+v", m.nav)
	}
	if ch < 0 {
		t.Fatalf("Unread channel row missing from nav after runtime unread; nav=%+v", m.nav)
	}
	if ch != hdr+1 {
		t.Fatalf("Unread channel row should immediately follow header; hdr=%d ch=%d", hdr, ch)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
