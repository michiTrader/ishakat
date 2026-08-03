// compact.go implements Step 12's client-side half of /compact (§10, §9.8):
// deciding what convo.PlanCompact already decided to replace is asked of
// compact_model in its own goroutine — Update must never block on the
// network (§7.1, the same rule submit's m.eng.Start already follows) — and
// the result lands back here as a compactDoneMsg. A model call that fails,
// or [compact].strategy = "drop-oldest" itself, falls back to
// convo.ApplySummary with a plain "discarded" marker instead of real prose:
// §10 is explicit that the JSONL must never claim a model wrote a summary it
// did not.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// compactState is ModeCompact's own state, live only while m.mode ==
// ModeCompact — the same convention confirmDialog and Picker already use.
type compactState struct {
	// switchTo is the model to switch to once compaction finishes, or ""
	// for a plain compaction with no swap attached (a bare /compact, or
	// the §10 auto-trigger). Mirrors confirmDialog's own "to" field.
	switchTo string

	// plan is what convo.PlanCompact decided to replace, computed once by
	// startCompact so the goroutine's eventual answer and finishCompact's
	// convo.ApplySummary call agree on exactly the same set of messages.
	plan convo.Plan

	// before is the conversation's context estimate at the moment
	// compaction started — the "142k" half of §9.8's "142k → 38k tokens"
	// line (rendered with a plain "->" rather than an arrow glyph: see
	// finishCompact's own comment on why).
	before int
}

// startCompact begins a compaction (§10): a bare /compact
// (slashrun.go), the confirm dialog's "compactar y cambiar" remedy
// (confirm.go's resolveConfirm), or the §10 auto-trigger (finishTurn).
// switchTo is the model to switch to once it finishes, or "" for a plain
// compaction.
//
// A Plan with nothing to replace — too few turns, or everything already
// summarized — needs no model call at all and is not an error: it falls
// straight through to finishSwitchAfterCompact, the exact path a
// successful compaction would also take once it is done.
func (m Root) startCompact(switchTo string) (tea.Model, tea.Cmd) {
	plan := convo.PlanCompact(m.conv.Messages, m.compactKeepLastTurns)
	if plan.Empty() {
		return m.finishSwitchAfterCompact(switchTo)
	}

	// [compact].strategy = "drop-oldest" skips the model call entirely
	// (§5.2's own documented meaning for that key), and so does a
	// compact_model that never resolved to a working engine at all (see
	// Root.compactEng's comment) — summarizing with no provider to ask
	// would be a lie about what actually happened. Both are instant: there
	// is nothing to wait on, so ModeCompact never opens for either.
	if m.compactStrategy == "drop-oldest" || m.compactEng == nil {
		before := m.conv.ContextTokens()
		m.applyDropOldestCompact(plan)
		return m.reportCompactDone(before, plan, switchTo)
	}

	before := m.conv.ContextTokens()
	m.compact = compactState{switchTo: switchTo, plan: plan, before: before}
	m.mode = ModeCompact
	m.animOffset = 0

	ctx, cancel := context.WithCancel(context.Background())
	m.compactCancel = cancel
	// wireModel: same §4.2 Ref-vs-WireID fix as startEngineTurn's — an
	// OmniRoute-served compact_model would otherwise get its Ref
	// ("omniroute/auto/coding") sent verbatim instead of its WireID
	// ("auto/coding"), the exact bug that made the main chat turn fail
	// with a misleading "no active credentials" 404.
	cmds := []tea.Cmd{summarizeCmd(ctx, m.compactEng, wireModel(m.cat, m.compactModel), m.conv.Messages, plan)}
	if !m.lay.AnimationsOff {
		cmds = append(cmds, tickAnim(m.fps))
	}
	return m, tea.Batch(cmds...)
}

// summarizeCmd wraps engine.Summarize as a tea.Cmd. Bubble Tea already runs
// every Cmd in its own goroutine (Program.handleCommands) before sending its
// Msg back through Update, so blocking on RunToCompletion in here — rather
// than spawning a second goroutine of our own, the way engine.Engine.Start
// does for a streamed turn — is already safe: nothing about this call needs
// StreamBuf's decoupling, since Summarize has exactly one answer to report,
// never a sequence of deltas to coalesce.
func summarizeCmd(ctx context.Context, eng *engine.Engine, model string, msgs []convo.Message, plan convo.Plan) tea.Cmd {
	return func() tea.Msg {
		summary, err := engine.Summarize(ctx, eng, model, msgs, plan)
		return compactDoneMsg{summary: summary, err: err}
	}
}

// updateCompact handles every message while mode == ModeCompact (§9.8).
// compactDoneMsg is handled one layer up, in updateDispatch, the same way
// modelChosenMsg is — both are global results that have to reach their
// handler regardless of which mode-specific switch a future case might add
// here. What is left for this switch is exactly updateBusy's shape: no
// textarea underneath to fall through to, only esc/ctrl+c doing anything.
func (m Root) updateCompact(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if keyPressString(key) == m.keys.Cancel {
			return m.cancelCompact()
		}
		return m, nil
	}
	return m, nil
}

// cancelCompact implements esc/ctrl+c while ModeCompact: cancels the
// in-flight compact_model call and returns to ModeChat without touching the
// conversation at all. Unlike cancelTurn there is no partial answer worth
// keeping — engine.Summarize either returns a whole summary or none, never
// something half-typed — so there is nothing left to drain before closing,
// which is why this needs no streamTickMsg-style follow-up tick.
func (m Root) cancelCompact() (tea.Model, tea.Cmd) {
	if m.compactCancel != nil {
		m.compactCancel()
		m.compactCancel = nil
	}
	m.compact = compactState{}
	m.mode = ModeChat
	m.animOffset = 0
	return m, nil
}

// finishCompact is compactDoneMsg's handler (§9.8): apply the summary
// engine.Summarize produced, or fall back to the same "discarded" marker
// startCompact's own drop-oldest short-circuit uses when [compact].on_error
// says to (the only value this package's config.example.toml documents).
// Any other on_error value — or none configured — surfaces the failure as a
// plain notice and leaves the conversation untouched: guessing a different
// remedy than what was configured would be worse than doing nothing.
func (m Root) finishCompact(summary string, err error) (tea.Model, tea.Cmd) {
	plan := m.compact.plan
	before := m.compact.before
	switchTo := m.compact.switchTo
	m.compactCancel = nil
	m.compact = compactState{}
	m.mode = ModeChat

	if err != nil {
		if m.compactOnError != "drop-oldest" {
			return m.slashNotice(m.lay.glyphs().warnMark + " compactación fallida: " + err.Error())
		}
		m.applyDropOldestCompact(plan)
		return m.reportCompactDone(before, plan, switchTo)
	}

	m.conv.ApplySummary(plan, summary, m.compactModel)
	return m.reportCompactDone(before, plan, switchTo)
}

// applyDropOldestCompact replaces exactly the messages plan already named
// (the ones compact_model was asked, or would have been asked, to
// summarize) with a marker that plainly says nothing was summarized —
// distinct from confirm.go's own applyDropOldest, which recomputes an
// unrelated set of indices from scratch to fit a destination model's
// window rather than to recover from a failed summary of a specific plan.
func (m *Root) applyDropOldestCompact(plan convo.Plan) {
	m.conv.ApplySummary(plan, "(turnos más viejos descartados: no se pudo generar un resumen)", "")
}

// reportCompactDone appends the §9.8 success line — "compactado: 142k -> 38k
// tokens (18 turnos -> 1 resumen + 4 turnos)" — and finishes whatever switch
// was waiting behind this compaction (finishSwitchAfterCompact, shared with
// the plan.Empty() short-circuit in startCompact).
//
// "->" is plain ASCII rather than "→": that arrow lives in the Unicode
// Arrows block, which unicodeGlyphs already draws from freely (see
// picker.go's scrollHint, "↑↓") — but a decorative glyph belongs in the
// glyphs table so both repertoires agree on it, and inventing one here for
// a single line is exactly what glyphs.go's own package comment warns
// against. Plain ASCII sidesteps the question and folds through unchanged
// either way.
func (m Root) reportCompactDone(before int, plan convo.Plan, switchTo string) (tea.Model, tea.Cmd) {
	after := m.conv.ContextTokens()
	replaced := countTurns(m.conv.Messages, plan.Replace)
	kept := countTurns(m.conv.Messages, plan.Keep)
	notice := fmt.Sprintf("compactado: %s -> %s tokens (%d turnos -> 1 resumen + %d turnos)",
		formatContextTokens(before), formatContextTokens(after), replaced, kept)
	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: "ishakat", text: notice, ts: time.Now(),
	})
	return m.finishSwitchAfterCompact(switchTo)
}

// countTurns counts how many of the messages at idx start a user turn —
// the "18 turnos" half of §9.8's success line counts turns, not raw message
// indices, the same unit convo.PlanCompact itself reasons in.
func countTurns(msgs []convo.Message, idx []int) int {
	n := 0
	for _, i := range idx {
		if i >= 0 && i < len(msgs) && msgs[i].Role == convo.RoleUser {
			n++
		}
	}
	return n
}

// finishSwitchAfterCompact closes ModeCompact and, if a model switch was
// waiting behind this compaction (confirm.go's "compactar y cambiar"),
// finishes it exactly the way applyModelChosen's unconflicted path always
// has — the same §4.6 confirmation line included.
func (m Root) finishSwitchAfterCompact(switchTo string) (tea.Model, tea.Cmd) {
	m.mode = ModeChat
	if switchTo == "" {
		return m, nil
	}
	m.model = switchTo
	m.footer.Model = switchTo
	return m.slashNotice(confirmLine(m.lay.glyphs(), switchTo))
}

// renderCompact draws the §9.8 "compactando" screen. Unlike renderLiveTurn
// there is no partial text to stream — engine.Summarize returns one
// finished string, never deltas — so the only moving part is the spinner
// strip CrushFrame already draws for a live turn, reused here verbatim.
func (m Root) renderCompact() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	turns := countTurns(m.conv.Messages, m.compact.plan.Replace)

	var b strings.Builder
	b.WriteString(" compactando contexto\n")
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " compactando %d turnos con %s\n", turns, m.compactModel)
	b.WriteString(" " + CrushFrame(m.lay, m.animOffset) + "\n")
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	b.WriteString(" esc cancela\n")
	return b.String()
}
