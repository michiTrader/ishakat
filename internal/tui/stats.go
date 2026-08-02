// stats.go implements /stats (§13, Step 13): a summary of the session's
// token accounting and its estimated cost, read straight from the running
// totals convo.Conversation.Usage already keeps — no separate bookkeeping
// of its own, so /stats can never disagree with what finishTurn recorded
// after every real turn.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
)

// runStats reports the session as a slashNotice: turn count, tokens in/out
// (plus cache read/write when a provider actually reported any — most
// never do, and a permanent "0" line would be noise), the context window
// currently in use, and a cost estimate priced against the active model's
// catalog.Cost when the catalog knows it.
func (m Root) runStats() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	usage := m.conv.Usage()
	turns := countAssistantTurns(m.conv.Messages)

	var b strings.Builder
	fmt.Fprintf(&b, "%s stats: %d turno(s)", g.assistantMark, turns)
	fmt.Fprintf(&b, "\n  tokens: %s in / %s out", formatStatTokens(usage.In), formatStatTokens(usage.Out))
	if usage.CacheRead > 0 || usage.CacheWrite > 0 {
		fmt.Fprintf(&b, " (cache: %s leidos / %s escritos)",
			formatStatTokens(usage.CacheRead), formatStatTokens(usage.CacheWrite))
	}
	if usage.Reasoning > 0 {
		fmt.Fprintf(&b, "\n  razonamiento: %s tokens", formatStatTokens(usage.Reasoning))
	}
	fmt.Fprintf(&b, "\n  contexto activo: %s tokens", formatStatTokens(m.conv.ContextTokens()))

	if model, ok := m.cat.Get(m.model); ok {
		fmt.Fprintf(&b, "\n  costo estimado: %s", statCostLabel(model, usage))
	}

	return m.slashNotice(b.String())
}

// countAssistantTurns is the number of completed assistant answers in the
// session — the "how many exchanges" number a user actually wants, as
// opposed to len(Messages), which also counts every user turn and any
// system message.
func countAssistantTurns(msgs []convo.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == convo.RoleAssistant {
			n++
		}
	}
	return n
}

// formatStatTokens reuses the footer's own "36k"/"900 tok" formatting
// (footer.go's formatTokens) so /stats and the footer never disagree about
// what a token count looks like.
func formatStatTokens(n int) string { return formatTokens(n) }

// statCostLabel prices usage against model.Cost (USD per million tokens,
// §4.2). A nil Cost is UNKNOWN, not free — same rule costLabel already
// applies in picker.go — so this reports "desconocido" instead of a
// confident $0.00 that would be a lie for a model whose price nobody has
// told the catalog about.
func statCostLabel(model catalog.Model, usage *convo.Usage) string {
	if model.Free() {
		return "$0.00 (modelo gratuito)"
	}
	if model.Cost == nil {
		return "desconocido (el catalogo no tiene precio para " + model.Ref + ")"
	}
	total := float64(usage.In)*model.Cost.In/1e6 +
		float64(usage.Out)*model.Cost.Out/1e6 +
		float64(usage.CacheRead)*model.Cost.CacheRead/1e6 +
		float64(usage.CacheWrite)*model.Cost.CacheWrite/1e6
	return fmt.Sprintf("$%.4f", total)
}
