// copy.go implements /copy and ctrl+y (§13, Step 13): put a past assistant
// response on the system clipboard via tea.SetClipboard, which speaks OSC52
// and therefore works over SSH — no X11/Wayland/pbcopy dependency, which
// matters on Termux where none of those exist.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// runCopy is /copy's and ctrl+y's shared implementation. args is the text
// after "/copy" — "" for ctrl+y and a bare /copy, or the 1-based count of
// responses back from the end ("/copy 2" is the answer before the last
// one). Anything that does not parse as a positive integer falls back to 1,
// the same "never a bare error" spirit §4.5's resolver already follows for
// slash commands: a typo in the argument should still copy something
// sensible rather than do nothing and say why.
func (m Root) runCopy(args string) (tea.Model, tea.Cmd) {
	n := parseCopyIndex(args)
	text, ok := nthLastAssistantText(m.conv, n)
	g := m.lay.glyphs()
	if !ok || strings.TrimSpace(text) == "" {
		return m.slashNotice(g.warnMark + " no hay " + copyTargetLabel(n) + " que copiar")
	}
	next, _ := m.slashNotice(g.assistantMark + " copiado: " + copyTargetLabel(n))
	return next, tea.SetClipboard(text)
}

// parseCopyIndex reads /copy's optional [n] argument. n <= 0 or unparsable
// both mean "the last one", which is also ctrl+y's only behaviour.
func parseCopyIndex(args string) int {
	args = strings.TrimSpace(args)
	if args == "" {
		return 1
	}
	if v, err := strconv.Atoi(args); err == nil && v > 0 {
		return v
	}
	return 1
}

// nthLastAssistantText walks c.Messages (the full history, not Active())
// backwards and returns the text of the nth assistant message counted from
// the end, n == 1 being the most recent one. The full history is
// deliberately used rather than Active(): a response /compact folded into a
// BlockSummary is still something the user watched happen on screen and may
// still want to copy verbatim, even though it no longer travels to the
// model on the next request.
func nthLastAssistantText(c convo.Conversation, n int) (string, bool) {
	if n < 1 {
		n = 1
	}
	count := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role != convo.RoleAssistant {
			continue
		}
		count++
		if count == n {
			return c.Messages[i].Text(), true
		}
	}
	return "", false
}

// copyTargetLabel names what /copy n is asking for, for both the "nothing
// to copy" warning and the success notice.
func copyTargetLabel(n int) string {
	if n <= 1 {
		return "una respuesta"
	}
	return fmt.Sprintf("la respuesta #%d desde el final", n)
}
