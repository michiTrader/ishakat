package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// The sample is printed by `doctor` to help a user judge their own console, so
// the ASCII one has to be drawable on the worst console there is.
func TestGlyphSampleStaysInsideTheRepertoire(t *testing.T) {
	view := strings.Join(GlyphSample(theme.GlyphsASCII), "\n")
	assertASCII(t, "glyph sample", view)
}

// The sample's whole value is that it shows what the interface will draw. If it
// could show a character the interface does not use — or miss one it does — a
// user would check it, see nothing wrong, and be sent looking in the wrong
// place. This walks the glyph table by reflection so that a field added to it
// later cannot silently stay out of the sample.
func TestGlyphSampleShowsEveryGlyphInTheTable(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		view := strings.Join(GlyphSample(set), "\n")
		table := reflect.ValueOf(glyphsFor(set))

		for i := 0; i < table.NumField(); i++ {
			name := table.Type().Field(i).Name
			var want string
			switch v := table.Field(i); v.Kind() {
			case reflect.String:
				want = v.String()
			case reflect.Slice: // spinner, []rune. Read rune by rune: the
				// fields are unexported, so reflect refuses Interface().
				var sb strings.Builder
				for j := 0; j < v.Len(); j++ {
					sb.WriteRune(rune(v.Index(j).Int()))
				}
				want = sb.String()
			default:
				t.Fatalf("glyph table field %s has unexpected kind %s", name, v.Kind())
			}
			if !strings.Contains(view, want) {
				t.Errorf("%v sample never draws %s (%q):\n%s", set, name, want, view)
			}
		}
	}
}

// The logo is where the report started, so it is the one thing the sample must
// not paraphrase.
func TestGlyphSampleOpensWithTheRealWordmark(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		want := Wordmark(Layout{Glyphs: set, Breakpoint: BPNormal, Width: 80})
		got := GlyphSample(set)
		if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
			t.Errorf("%v sample does not open with the wordmark:\nwant %q\ngot  %q", set, want, got)
		}
	}
}
