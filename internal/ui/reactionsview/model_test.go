package reactionsview

import (
	"strings"
	"testing"
)

func sampleTabs() []EmojiTab {
	return []EmojiTab{
		{Emoji: "thumbsup", Count: 2, Users: []string{"alice", "bob"}},
		{Emoji: "tada", Count: 1, Users: []string{"carol"}},
		{Emoji: "eyes", Count: 0, Users: nil},
	}
}

func TestOpenSetsVisibleAndDefaultsToFirstTab(t *testing.T) {
	m := New()
	m.Open("hi there", sampleTabs())
	if !m.IsVisible() {
		t.Fatal("Open: expected IsVisible=true")
	}
	if got := m.SelectedTab(); got != 0 {
		t.Fatalf("SelectedTab after Open = %d, want 0", got)
	}
	if got := m.UserOffset(); got != 0 {
		t.Fatalf("UserOffset after Open = %d, want 0", got)
	}
}

func TestOpenWithEmptyTabsDoesNothing(t *testing.T) {
	m := New()
	m.Open("hi", nil)
	if m.IsVisible() {
		t.Fatal("Open with empty tabs should not become visible")
	}
}

func TestHandleKeyEscClosesOverlay(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())
	if closed := m.HandleKey("esc"); !closed {
		t.Fatal("esc should close and return true")
	}
	if m.IsVisible() {
		t.Fatal("overlay still visible after esc")
	}
}

func TestHandleKeyQClosesOverlay(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())
	if closed := m.HandleKey("q"); !closed {
		t.Fatal("q should close and return true")
	}
	if m.IsVisible() {
		t.Fatal("overlay still visible after q")
	}
}

func TestHandleKeyLRCyclesTabs(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())

	m.HandleKey("l")
	if m.SelectedTab() != 1 {
		t.Fatalf("after l, SelectedTab = %d, want 1", m.SelectedTab())
	}
	m.HandleKey("l")
	if m.SelectedTab() != 2 {
		t.Fatalf("after ll, SelectedTab = %d, want 2", m.SelectedTab())
	}
	m.HandleKey("l")
	if m.SelectedTab() != 0 {
		t.Fatalf("after lll, SelectedTab = %d, want 0 (wrap)", m.SelectedTab())
	}
	m.HandleKey("h")
	if m.SelectedTab() != 2 {
		t.Fatalf("after h (wrap back), SelectedTab = %d, want 2", m.SelectedTab())
	}
}

func TestHandleKeyTabShiftTabAlsoCyclesTabs(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())
	m.HandleKey("tab")
	if m.SelectedTab() != 1 {
		t.Fatalf("after tab, SelectedTab = %d, want 1", m.SelectedTab())
	}
	m.HandleKey("shift+tab")
	if m.SelectedTab() != 0 {
		t.Fatalf("after shift+tab, SelectedTab = %d, want 0", m.SelectedTab())
	}
}

func TestSwitchingTabsResetsUserOffset(t *testing.T) {
	users := make([]string, 30)
	for i := range users {
		users[i] = "u"
	}
	tabs := []EmojiTab{
		{Emoji: "a", Count: 30, Users: users},
		{Emoji: "b", Count: 1, Users: []string{"x"}},
	}
	m := New()
	m.Open("msg", tabs)
	for i := 0; i < 5; i++ {
		m.HandleKey("j")
	}
	if m.UserOffset() != 5 {
		t.Fatalf("UserOffset after 5x j = %d, want 5", m.UserOffset())
	}
	m.HandleKey("l")
	if m.UserOffset() != 0 {
		t.Fatalf("UserOffset after switching tabs = %d, want 0", m.UserOffset())
	}
}

func TestScrollDownClampsAtMax(t *testing.T) {
	users := make([]string, 20)
	for i := range users {
		users[i] = "u"
	}
	tabs := []EmojiTab{{Emoji: "a", Count: 20, Users: users}}
	m := New()
	m.Open("msg", tabs)
	// max offset = 20 - maxVisibleUsers(12) = 8
	for i := 0; i < 50; i++ {
		m.HandleKey("j")
	}
	if got := m.UserOffset(); got != 8 {
		t.Fatalf("UserOffset clamped = %d, want 8", got)
	}
	for i := 0; i < 50; i++ {
		m.HandleKey("k")
	}
	if got := m.UserOffset(); got != 0 {
		t.Fatalf("UserOffset after k clamp = %d, want 0", got)
	}
}

func TestScrollDoesNotMoveWhenListFits(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())
	m.HandleKey("j")
	if m.UserOffset() != 0 {
		t.Fatalf("UserOffset when list fits = %d, want 0", m.UserOffset())
	}
}

func TestGAndShiftGJumpToEnds(t *testing.T) {
	users := make([]string, 30)
	for i := range users {
		users[i] = "u"
	}
	tabs := []EmojiTab{{Emoji: "a", Count: 30, Users: users}}
	m := New()
	m.Open("msg", tabs)
	m.HandleKey("G")
	if got := m.UserOffset(); got != 30-12 {
		t.Fatalf("UserOffset after G = %d, want %d", got, 30-12)
	}
	m.HandleKey("g")
	if got := m.UserOffset(); got != 0 {
		t.Fatalf("UserOffset after g = %d, want 0", got)
	}
}

func TestRenderBoxNonEmptyWithTabs(t *testing.T) {
	m := New()
	m.Open("hello", sampleTabs())
	out := m.View(80)
	if out == "" {
		t.Fatal("View returned empty string with visible overlay")
	}
}

func TestTabStripRendersUnicodeGlyph(t *testing.T) {
	m := New()
	m.Open("hi", []EmojiTab{{Emoji: "thumbsup", Count: 2, Users: []string{"alice"}}})
	out := m.View(80)
	// image mode is off in tests; fallback path should resolve :thumbsup: to 👍.
	if !strings.Contains(out, "👍") {
		t.Fatalf("expected Unicode glyph for thumbsup in tab strip, got: %s", out)
	}
	if strings.Contains(out, ":thumbsup:") {
		t.Fatalf("expected shortcode to be replaced with a glyph, got: %s", out)
	}
}

func TestRenderBoxEmptyWhenHidden(t *testing.T) {
	m := New()
	out := m.View(80)
	if out != "" {
		t.Fatalf("View on hidden overlay = %q, want empty", out)
	}
}

func TestHandleKeyOnHiddenIsNoop(t *testing.T) {
	m := New()
	if closed := m.HandleKey("l"); closed {
		t.Fatal("HandleKey on hidden returned closed=true")
	}
}

func TestCloseResetsState(t *testing.T) {
	m := New()
	m.Open("msg", sampleTabs())
	m.HandleKey("l")
	m.Close()
	if m.IsVisible() || m.SelectedTab() != 0 || m.UserOffset() != 0 || m.Tabs() != nil {
		t.Fatalf("Close did not reset state: visible=%v tab=%d off=%d tabs=%v",
			m.IsVisible(), m.SelectedTab(), m.UserOffset(), m.Tabs())
	}
}
