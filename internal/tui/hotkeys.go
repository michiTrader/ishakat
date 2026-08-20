package tui

import (
	"fmt"
	"strings"
)

// renderHotkeys draws the roadmap-F3 dedicated shortcuts overlay (ModeHotkeys).
//
// It exists as its own screen, reachable with /hotkeys, instead of being
// folded into renderHelp's own "atajos" section, because F3's roadmap
// wording asks for a shortcuts screen of its own — "keep our ESC-dismissable
// overlay style" — distinct from /help's combined commands+shortcuts
// listing. It deliberately does not duplicate the shortcut data: it calls
// the exact same m.helpShortcuts() renderHelp already calls, so /help's
// embedded "atajos" section and this dedicated screen can never drift from
// each other, or from the loaded keymap (RC-1's fix), since both read the
// same Map through the same function.
//
// Only F3's "dedicated overlay generated from the keymap" half is
// implemented here. F3's other half — "overlays must open while the agent
// is working, without blocking input" — needs the non-modal ModeBusy
// eventing infrastructure the roadmap's own W2 section builds, which has
// not landed yet in the approved W0→W1→W3→W2→W4→W5→W6 sequence. Today
// ModeHotkeys is reachable from ModeChat exactly the same way ModeHelp
// already is (neither can open mid-turn) — that is an existing, unchanged
// constraint carried over from /help, not a new limitation introduced by
// this file.
//
// The layout follows renderHelp's own idiom: width measured once from
// m.lay.ContentWidth() (F14, so this screen does not introduce a new
// hardcoded-width regression the moment it is written), one helpHeading
// rule line, then the shortcut rows, then a dismiss line.
func (m Root) renderHotkeys() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	var b strings.Builder
	b.WriteString(helpHeading(g, width, "ishakat "+g.dot+" atajos") + "\n\n")
	for _, line := range m.helpShortcuts() {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(fmt.Sprintf("\n %s desplazar %s esc volver", g.scrollHint, g.dot))
	return b.String()
}
