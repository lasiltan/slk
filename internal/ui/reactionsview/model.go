// Package reactionsview implements the read-only "list reactions" overlay
// bound to R in normal mode. It shows the highlighted message's text at
// the top, a tab strip with one tab per emoji on the message, and the
// list of display names that reacted with the active tab's emoji.
//
// The overlay takes a snapshot of {emoji, users} at Open() time and does
// not observe live reaction events while visible. Lifetime is short
// (Esc/q closes it) so staleness is acceptable, and snapshotting avoids
// tab indices shifting under the user when reactions arrive.
package reactionsview

import (
	"image/color"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	kyoemoji "github.com/kyokomi/emoji/v2"
	"github.com/muesli/reflow/truncate"

	slkemoji "github.com/gammons/slk/internal/emoji"
	imgpkg "github.com/gammons/slk/internal/image"
	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/overlay"
	"github.com/gammons/slk/internal/ui/styles"
)

// EmojiContext bundles emoji-image rendering dependencies. Set once at
// startup; updated when CustomEmojisLoadedMsg arrives via SetEmojiCustoms.
// Mirrors reactionpicker.EmojiContext / messages.EmojiContext.
type EmojiContext struct {
	PlaceCtx slkemoji.PlaceContext
	Cells    int
	Customs  map[string]string
}

// Reactor is one entry in EmojiTab.Users: the user ID (used to derive a
// stable per-user color) and the resolved display name (or the raw ID
// again if the name was not known at snapshot time).
type Reactor struct {
	ID   string
	Name string
}

// EmojiTab is one entry in the tab strip: an emoji name and the
// pre-resolved display names of users who reacted with it.
type EmojiTab struct {
	Emoji string    // emoji name without colons, e.g. "thumbsup"
	Count int       // total reactor count (may exceed len(Users) if some IDs missing)
	Users []Reactor // reactors in arrival order
}

// Model is the reactions view overlay.
type Model struct {
	visible        bool
	messagePreview string
	tabs           []EmojiTab
	selectedTab    int
	userOffset     int // top of the visible window in the active tab's user list
	emojiCtx       EmojiContext
}

// SetEmojiContext configures emoji-image rendering for the tab strip.
func (m *Model) SetEmojiContext(ctx EmojiContext) {
	if ctx.Cells != 1 && ctx.Cells != 2 {
		ctx.Cells = 2
	}
	m.emojiCtx = ctx
}

// SetEmojiCustoms updates the customs map without changing PlaceCtx/Cells.
func (m *Model) SetEmojiCustoms(customs map[string]string) {
	m.emojiCtx.Customs = customs
}

// New constructs an empty reactions-view overlay.
func New() *Model {
	return &Model{}
}

// Open shows the overlay with the given message preview and per-emoji
// tabs. If tabs is empty the overlay does not become visible.
func (m *Model) Open(messagePreview string, tabs []EmojiTab) {
	if len(tabs) == 0 {
		return
	}
	m.messagePreview = messagePreview
	m.tabs = tabs
	m.selectedTab = 0
	m.userOffset = 0
	m.visible = true
}

// Close hides the overlay and resets state.
func (m *Model) Close() {
	m.visible = false
	m.messagePreview = ""
	m.tabs = nil
	m.selectedTab = 0
	m.userOffset = 0
}

// IsVisible reports whether the overlay is currently shown.
func (m *Model) IsVisible() bool {
	return m.visible
}

// SelectedTab returns the currently focused tab index. Exported for tests.
func (m *Model) SelectedTab() int {
	return m.selectedTab
}

// UserOffset returns the top-of-window index for the active tab. Exported for tests.
func (m *Model) UserOffset() int {
	return m.userOffset
}

// Tabs returns the current snapshot of tabs. Exported for tests.
func (m *Model) Tabs() []EmojiTab {
	return m.tabs
}

// HandleKey processes a key event. Returns true if the overlay closed.
func (m *Model) HandleKey(keyStr string) bool {
	if !m.visible {
		return false
	}
	switch keyStr {
	case "esc", "escape", "q":
		m.Close()
		return true
	case "h", "left", "shift+tab":
		m.prevTab()
	case "l", "right", "tab":
		m.nextTab()
	case "j", "down":
		m.scrollDown()
	case "k", "up":
		m.scrollUp()
	case "g":
		m.userOffset = 0
	case "G":
		m.userOffset = m.maxOffset()
	}
	return false
}

func (m *Model) prevTab() {
	if len(m.tabs) == 0 {
		return
	}
	m.selectedTab = (m.selectedTab - 1 + len(m.tabs)) % len(m.tabs)
	m.userOffset = 0
}

func (m *Model) nextTab() {
	if len(m.tabs) == 0 {
		return
	}
	m.selectedTab = (m.selectedTab + 1) % len(m.tabs)
	m.userOffset = 0
}

func (m *Model) scrollDown() {
	max := m.maxOffset()
	if m.userOffset < max {
		m.userOffset++
	}
}

func (m *Model) scrollUp() {
	if m.userOffset > 0 {
		m.userOffset--
	}
}

const maxVisibleUsers = 12

func (m *Model) maxOffset() int {
	if m.selectedTab >= len(m.tabs) {
		return 0
	}
	n := len(m.tabs[m.selectedTab].Users)
	if n <= maxVisibleUsers {
		return 0
	}
	return n - maxVisibleUsers
}

// View renders the overlay box (no background composite). Exposed for tests.
func (m *Model) View(termWidth int) string {
	return m.renderBox(termWidth)
}

// ViewOverlay composites the overlay on top of background.
func (m *Model) ViewOverlay(termWidth, termHeight int, background string) string {
	if !m.visible {
		return background
	}
	box := m.renderBox(termWidth)
	if box == "" {
		return background
	}
	result := overlay.DimmedOverlay(termWidth, termHeight, background, box, 0.5)
	lines := strings.Split(result, "\n")
	if len(lines) > termHeight {
		lines = lines[:termHeight]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderBox(termWidth int) string {
	if !m.visible || len(m.tabs) == 0 {
		return ""
	}

	overlayWidth := termWidth * 50 / 100
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	if overlayWidth > 70 {
		overlayWidth = 70
	}
	innerWidth := overlayWidth - 4 // border + padding

	bg := styles.Background

	title := lipgloss.NewStyle().
		Background(bg).
		Foreground(styles.Primary).
		Bold(true).
		Render("Reactions")

	preview := m.messagePreview
	if preview == "" {
		preview = "(no message text)"
	}
	previewLine := preview
	if lipgloss.Width(previewLine) > innerWidth {
		previewLine = truncate.StringWithTail(previewLine, uint(innerWidth), "…")
	}
	previewStyled := lipgloss.NewStyle().
		Background(bg).
		Foreground(styles.TextMuted).
		Italic(true).
		Render(previewLine)

	var pendingFlushes []func(io.Writer) error
	tabStrip := m.renderTabStrip(innerWidth, bg, &pendingFlushes)

	users := m.tabs[m.selectedTab].Users
	totalUsers := len(users)
	visible := totalUsers
	if visible > maxVisibleUsers {
		visible = maxVisibleUsers
	}
	start := m.userOffset
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > totalUsers {
		end = totalUsers
	}

	showScrollbar := totalUsers > maxVisibleUsers
	rowWidth := innerWidth - 1
	if !showScrollbar {
		rowWidth = innerWidth
	}

	var thumbStart, thumbEnd int
	if showScrollbar {
		thumbHeight := visible * visible / totalUsers
		if thumbHeight < 1 {
			thumbHeight = 1
		}
		denom := totalUsers - visible
		if denom < 1 {
			denom = 1
		}
		thumbStart = start * (visible - thumbHeight) / denom
		if thumbStart < 0 {
			thumbStart = 0
		}
		if thumbStart > visible-thumbHeight {
			thumbStart = visible - thumbHeight
		}
		thumbEnd = thumbStart + thumbHeight
	}
	thumbStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.Primary)
	trackStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.Border)

	var rows []string
	for i := start; i < end; i++ {
		r := users[i]
		name := r.Name
		if lipgloss.Width(name) > rowWidth {
			name = truncate.StringWithTail(name, uint(rowWidth), "…")
		}
		fg := styles.UserColor(r.ID)
		row := lipgloss.NewStyle().
			Background(bg).
			Foreground(fg).
			Bold(true).
			Width(rowWidth).
			MaxWidth(rowWidth).
			Render(name)
		if showScrollbar {
			rel := i - start
			if rel >= thumbStart && rel < thumbEnd {
				row += thumbStyle.Render("█")
			} else {
				row += trackStyle.Render("│")
			}
		}
		rows = append(rows, row)
	}

	if totalUsers == 0 {
		rows = append(rows, lipgloss.NewStyle().
			Background(bg).
			Foreground(styles.TextMuted).
			Italic(true).
			Render("(no users)"))
	}

	footer := lipgloss.NewStyle().
		Background(bg).
		Foreground(styles.TextMuted).
		Render("h/l tab · j/k scroll · esc close")

	content := title + "\n" + previewStyled + "\n\n" + tabStrip + "\n" + strings.Join(rows, "\n") + "\n\n" + footer

	content = messages.ReapplyBgAfterResets(content, messages.BgANSI()+messages.FgANSI())

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		BorderBackground(bg).
		Background(bg).
		Padding(1, 1).
		Width(overlayWidth).
		Render(content)

	// Fire any kitty image upload callbacks the per-emoji Place calls
	// produced. Mirrors the reactionpicker pattern; most are no-ops in
	// steady state (the messages pane already uploaded via the shared
	// Registry) but the overlay still owns the fire to handle the case
	// where it is the first surface to reference a given emoji.
	for _, fl := range pendingFlushes {
		_ = fl(imgpkg.KittyOutput)
	}

	return box
}

func (m *Model) renderTabStrip(innerWidth int, bg color.Color, flushes *[]func(io.Writer) error) string {
	bgStyle := lipgloss.NewStyle().Background(bg)
	activeStyle := lipgloss.NewStyle().
		Background(bg).
		Foreground(styles.Primary).
		Bold(true)
	inactiveStyle := lipgloss.NewStyle().
		Background(bg).
		Foreground(styles.TextMuted)

	imageOK := slkemoji.ImageModeActive() && m.emojiCtx.PlaceCtx.Fetcher != nil
	cells := m.emojiCtx.Cells
	if cells <= 0 {
		cells = 2
	}

	var parts []string
	for i, tab := range m.tabs {
		glyph := renderEmojiGlyph(tab.Emoji, imageOK, cells, m.emojiCtx, flushes)
		label := glyph + " " + strconv.Itoa(tab.Count)
		if i == m.selectedTab {
			parts = append(parts, activeStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, inactiveStyle.Render(" "+label+" "))
		}
	}
	line := strings.Join(parts, bgStyle.Render(" "))
	if lipgloss.Width(line) > innerWidth {
		line = truncate.StringWithTail(line, uint(innerWidth), "…")
	}
	return line
}

// renderEmojiGlyph resolves a reaction emoji name into a renderable glyph:
// image placement when image-mode is on and a URL resolves, otherwise the
// Unicode codepoint via kyokomi/emoji, falling back to the shortcode text.
// Mirrors the messages-pane reaction-pill loop.
func renderEmojiGlyph(name string, imageOK bool, cells int, ctx EmojiContext, flushes *[]func(io.Writer) error) string {
	if imageOK {
		if url, ok := slkemoji.URLForShortcode(name, ctx.Customs); ok {
			if placement, flush, ok := slkemoji.Place(ctx.PlaceCtx, url, cells); ok {
				if flush != nil && flushes != nil {
					*flushes = append(*flushes, flush)
				}
				return placement
			}
		}
	}
	// Legacy fallback. Strip skin tone — the glyph renderer can't handle
	// multi-codepoint ZWJ / skin-tone sequences reliably across terminal
	// fonts (same workaround as the reaction-pill loop).
	legacyName := slkemoji.StripSkinTone(name)
	resolved := kyoemoji.Sprint(":" + legacyName + ":")
	if slkemoji.ShouldRenderUnicode(resolved) {
		return resolved
	}
	return ":" + legacyName + ":"
}

