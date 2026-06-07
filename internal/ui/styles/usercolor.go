// internal/ui/styles/usercolor.go
package styles

import (
	"hash/fnv"
	"image/color"
	"math"
	"sync"

	"charm.land/lipgloss/v2"
)

// UsernameStyleFor returns the Username style with its foreground replaced
// by a stable per-user color derived from userID. Background, bold, and
// other attributes from the active theme's Username style are preserved,
// so themes that customize Username (e.g. italics, different bg) still
// take effect.
func UsernameStyleFor(userID string) lipgloss.Style {
	if userID == "" {
		return Username
	}
	return Username.Foreground(UserColor(userID))
}

// userColorSaturations defines the small palette the hash indexes into
// alongside hue. Lightness comes from userColorLightnessesFor, which
// branches on the active theme's background luminance so colors stay
// legible on both light and dark themes.
//
// 360 hues × 3 sats × 3 lightnesses = 3240 candidate (H,S,L) tuples;
// after rgbHex quantization the practical distinct-color count is
// comfortably above 1000.
var userColorSaturations = [3]float64{0.55, 0.65, 0.75}

var (
	userColorLightnessesDark  = [3]float64{0.60, 0.70, 0.80} // light fg on dark bg
	userColorLightnessesLight = [3]float64{0.25, 0.35, 0.45} // dark fg on light bg
)

var userColorCache sync.Map // map[string]color.Color

// resetUserColorCache invalidates memoized colors so a subsequent
// theme change picks fresh values from the active lightness palette.
// Called by Apply().
func resetUserColorCache() {
	userColorCache = sync.Map{}
}

// userColorLightnessesFor returns the lightness palette appropriate for
// the current Background. Dark backgrounds get lighter foregrounds and
// vice versa, so names stay readable on every built-in theme.
func userColorLightnessesFor() [3]float64 {
	if isLightBackground(Background) {
		return userColorLightnessesLight
	}
	return userColorLightnessesDark
}

// isLightBackground returns true if c's relative luminance is above 0.5.
// Uses the sRGB luma approximation (0.299/0.587/0.114) which is fast and
// sufficient for the dark/light branch decision.
func isLightBackground(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	// RGBA returns 16-bit channels; collapse to [0,1].
	rf := float64(r>>8) / 255
	gf := float64(g>>8) / 255
	bf := float64(b>>8) / 255
	return 0.299*rf+0.587*gf+0.114*bf > 0.5
}

// UserColor returns a stable foreground color derived from userID. The
// same userID always yields the same color across runs (FNV-32a is
// deterministic), and the (H,S,L) grid yields ~1000+ visually distinct
// results across the range of Slack user IDs.
//
// Empty userID returns Primary so synthetic / system rows keep the
// existing username color.
func UserColor(userID string) color.Color {
	if userID == "" {
		return Primary
	}
	if cached, ok := userColorCache.Load(userID); ok {
		return cached.(color.Color)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	sum := h.Sum32()

	lightnesses := userColorLightnessesFor()
	hue := float64(sum % 360)
	sat := userColorSaturations[(sum/360)%uint32(len(userColorSaturations))]
	light := lightnesses[(sum/(360*uint32(len(userColorSaturations))))%uint32(len(lightnesses))]

	r, g, b := hslToRGB(hue, sat, light)
	c := lipgloss.Color(rgbHex(r, g, b))
	userColorCache.Store(userID, c)
	return c
}

// hslToRGB converts an HSL triple (H in [0,360), S and L in [0,1]) into
// 8-bit RGB. Standard formula; matches the CSS HSL spec.
func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	c := (1 - math.Abs(2*l-1)) * s
	hp := h / 60.0
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r1, g1, b1 float64
	switch {
	case hp < 1:
		r1, g1, b1 = c, x, 0
	case hp < 2:
		r1, g1, b1 = x, c, 0
	case hp < 3:
		r1, g1, b1 = 0, c, x
	case hp < 4:
		r1, g1, b1 = 0, x, c
	case hp < 5:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	m := l - c/2
	return uint8(math.Round((r1 + m) * 255)),
		uint8(math.Round((g1 + m) * 255)),
		uint8(math.Round((b1 + m) * 255))
}
