package emoji

import (
	"reflect"
	"testing"
)

// text builds a TokenText for table-driven test brevity.
func text(s string) Token { return Token{Kind: TokenText, Text: s} }

// emoji builds a TokenEmoji for table-driven test brevity.
func emoji(plain, url string) Token { return Token{Kind: TokenEmoji, Text: plain, URL: url} }

func TestResolveEmojiToTokens_Trivial(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Token
	}{
		{"empty", "", nil},
		{"ascii only", "hello world", []Token{text("hello world")}},
		{"only spaces", "   ", []Token{text("   ")}},
		{"newlines preserved", "line one\nline two", []Token{text("line one\nline two")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveEmojiToTokens(c.in, nil)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ResolveEmojiToTokens(%q) = %#v\n want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestResolveEmojiToTokens_Shortcodes(t *testing.T) {
	thumbURL := CDNBaseURL + "1f44d.png"     // :thumbsup:
	heartURL := CDNBaseURL + "2764.png"      // :heart: (VS16 stripped)
	rocketURL := CDNBaseURL + "1f680.png"    // :rocket:
	customParrot := "https://emoji.slack-edge.com/T01/party_parrot/abc.gif"
	customs := map[string]string{
		"party_parrot": customParrot,
		"alias_for_rocket": "alias:rocket",
	}

	cases := []struct {
		name string
		in   string
		want []Token
	}{
		{
			"shortcode at start",
			":thumbsup: nice",
			[]Token{emoji(":thumbsup:", thumbURL), text(" nice")},
		},
		{
			"shortcode at end",
			"nice :thumbsup:",
			[]Token{text("nice "), emoji(":thumbsup:", thumbURL)},
		},
		{
			"shortcode in middle",
			"a :heart: b",
			[]Token{text("a "), emoji(":heart:", heartURL), text(" b")},
		},
		{
			"two shortcodes with text between",
			":heart: and :rocket:",
			[]Token{emoji(":heart:", heartURL), text(" and "), emoji(":rocket:", rocketURL)},
		},
		{
			"adjacent shortcodes (no separator)",
			":heart::rocket:",
			[]Token{emoji(":heart:", heartURL), emoji(":rocket:", rocketURL)},
		},
		{
			"unknown shortcode passes through as text",
			":not_an_emoji_xyz: hello",
			[]Token{text(":not_an_emoji_xyz: hello")},
		},
		{
			"broken shortcode (missing closing colon)",
			":heart still text",
			[]Token{text(":heart still text")},
		},
		{
			"workspace custom",
			"hello :party_parrot:",
			[]Token{text("hello "), emoji(":party_parrot:", customParrot)},
		},
		{
			"alias resolves to builtin",
			"go :alias_for_rocket: go",
			[]Token{text("go "), emoji(":alias_for_rocket:", rocketURL), text(" go")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveEmojiToTokens(c.in, customs)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ResolveEmojiToTokens(%q):\n got  %#v\n want %#v", c.in, got, c.want)
			}
		})
	}
}
