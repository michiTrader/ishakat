// suggest.go implements Step 25's client-side half of §19.7's
// "crystallization by observation": the ModeSuggest overlay that offers to
// turn a repeated pattern (already detected by internal/evolve's own
// ledger/suggest.go, the read side) into a real tool, at the end of a turn
// that settled cleanly, following civility rule 1 ("never mid-task").
//
// This package never touches usage.jsonl or suggest-state.json directly —
// see EvolveStore's own comment for the §6.1 boundary that draws — and it
// never constructs a tool_create call by hand either: acceptSuggestion
// hands the model a natural-language description of the pattern instead
// and lets the model itself propose the name/manifest, exactly the same
// way any other agent-initiated tool_create would arrive, so the existing
// ModeToolApprove gate 2 dialog (toolapprove.go) is the only approval UI
// this feature ever needs.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/evolve"
)

// EvolveStore is §19.7's own read/write seam over usage.jsonl (the
// ledger of observed patterns) and suggest-state.json (the suggestion
// feature's own budget/decay bookkeeping) — the same "package never
// touches the filesystem itself" boundary Recorder and SessionLister
// (session.go) already draw for their own concerns (§6.1). internal/app
// is expected to implement this over evolve.LoadLedger/evolve.Save,
// evolve.LoadSuggestState/evolve.SaveSuggestState and
// config.SetEvolveMode, the same three-file, best-effort pattern
// internal/app/ledger.go already follows for the headless path.
type EvolveStore interface {
	LoadLedger() (*evolve.Ledger, error)
	SaveLedger(*evolve.Ledger) error
	LoadSuggestState() (*evolve.SuggestState, error)
	SaveSuggestState(*evolve.SuggestState) error
	// Decay drops [tools.evolve].mode to "on_request" (§19.7 rule 4).
	// Kept on the interface, not inlined in dismissSuggestion, so this
	// package never imports internal/config's write path directly —
	// the same reason SessionLister exists instead of a raw *convo.Store.
	Decay() error
}

// suggestOption is one selectable row of the ModeSuggest dialog. kind
// drives resolveSuggest's dispatch; label is what renderSuggest draws —
// §19.7's own mockup shows literal "[t]"/"[v]"/"[n]" letters, but this
// package's every other overlay (confirm.go, toolapprove.go) resolves
// its selected row through up/down + m.keys.Submit/Cancel, never a
// literal letter key, so the bracketed letters survive only as row
// text, not as bindings.
type suggestOption struct {
	kind  string // "accept" | "detail" | "dismiss"
	label string
}

// suggestState is ModeSuggest's own live state (mirrors compactState/
// confirmDialog's shape), holding exactly the one SuggestionCandidate
// currently on screen plus the cursor and the detail-toggle.
type suggestState struct {
	candidate evolve.SuggestionCandidate
	detail    bool
	sel       int
}

func newSuggestState(cand evolve.SuggestionCandidate) suggestState {
	return suggestState{candidate: cand}
}

// options is computed rather than stored so the "ver el código"/"ocultar
// detalle" label always matches the current detail flag without a
// second place having to remember to keep them in sync.
func (s suggestState) options() []suggestOption {
	detailLabel := "ver el código"
	if s.detail {
		detailLabel = "ocultar detalle"
	}
	return []suggestOption{
		{kind: "accept", label: "crearla"},
		{kind: "detail", label: detailLabel},
		{kind: "dismiss", label: "no, ni ahora ni después"},
	}
}

func (s suggestState) moveSel(delta int) suggestState {
	n := len(s.options())
	s.sel = ((s.sel+delta)%n + n) % n
	return s
}

func (s suggestState) selected() suggestOption { return s.options()[s.sel] }

// checkSuggest is checkEndOfTurn's second half (root.go): §19.7's own
// end-of-turn hook, called only once a turn has settled fully back into
// ModeChat. A nil evolveStore (the ordinary case for
// [tools.evolve].mode != "suggest", or no TTY — rule 5) means the
// feature is inert: this is the one and only place that nil is checked,
// so every other function below can assume a working store.
func (m Root) checkSuggest() (tea.Model, tea.Cmd) {
	if m.evolveStore == nil {
		return m, nil
	}
	ledger, err := m.evolveStore.LoadLedger()
	if err != nil {
		return m, nil
	}
	state, err := m.evolveStore.LoadSuggestState()
	if err != nil {
		return m, nil
	}
	today := time.Now().UTC().Format("2006-01-02")
	state.RollWeek(today)
	decision := evolve.DecideSuggestion(
		ledger.Records, *state, m.evolveThresholds,
		m.suggestSessionCount, m.suggestPerSession, m.suggestPerWeek,
	)
	if !decision.Offer {
		// RollWeek may have just reset the window even though nothing
		// is offered this turn — persist that roll so the next check
		// (possibly days later) does not redo it from a stale window.
		_ = m.evolveStore.SaveSuggestState(state)
		return m, nil
	}
	return m.startSuggest(decision.Candidate, *state)
}

// startSuggest opens the dialog and records the suggestion as *shown*
// (RecordShown) — rule 3's own distinction between an opportunity merely
// detected (NextSuggestion returning ok=true) and one actually offered,
// which is the only kind that counts against the session/week budgets.
func (m Root) startSuggest(cand evolve.SuggestionCandidate, state evolve.SuggestState) (tea.Model, tea.Cmd) {
	state.RecordShown()
	if m.evolveStore != nil {
		_ = m.evolveStore.SaveSuggestState(&state)
	}
	m.suggest = newSuggestState(cand)
	m.suggestSessionCount++
	m.mode = ModeSuggest
	m.animOffset = 0
	return m, nil
}

// updateSuggest follows this package's own established convention (see
// confirm.go/toolapprove.go): up/down moves the cursor, m.keys.Submit
// resolves the selected row, m.keys.Cancel closes the dialog without
// counting as a rejection (esc is "not now", not "no, never" — §19.7's
// own third row is the only path that records a rejection).
func (m Root) updateSuggest(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.cancelSuggest()
	case "up":
		m.suggest = m.suggest.moveSel(-1)
		return m, nil
	case "down":
		m.suggest = m.suggest.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveSuggest()
	}
	return m, nil
}

// cancelSuggest is esc: closes the overlay with no side effect at all —
// not a rejection, not an acceptance, simply "not now". The pattern
// remains eligible and may be offered again a future turn, budgets
// permitting.
func (m Root) cancelSuggest() (tea.Model, tea.Cmd) {
	m.suggest = suggestState{}
	m.mode = ModeChat
	return m, nil
}

func (m Root) resolveSuggest() (tea.Model, tea.Cmd) {
	switch m.suggest.selected().kind {
	case "detail":
		m.suggest.detail = !m.suggest.detail
		return m, nil
	case "dismiss":
		return m.dismissSuggestion()
	default: // "accept"
		return m.acceptSuggestion()
	}
}

// acceptSuggestion is "[t] crearla": rather than build a tool_create
// call by hand — a bare SuggestionCandidate has only Pattern/N/Last, no
// name or manifest yet, see evolve/suggest.go's own doc comment on why
// this is a narrower question than gate 1 — this appends a plain
// user-role prompt describing the pattern and asks the model to call
// tool_create itself (origin="agent"), then reuses startEngineTurn, the
// exact same machinery /retry already drives. The resulting tool_create
// call is intercepted by the existing ModeToolApprove gate 2 dialog with
// no new approval UI required.
func (m Root) acceptSuggestion() (tea.Model, tea.Cmd) {
	cand := m.suggest.candidate
	m.suggest = suggestState{}

	if m.evolveStore != nil {
		if state, err := m.evolveStore.LoadSuggestState(); err == nil {
			state.RecordAcceptance()
			_ = m.evolveStore.SaveSuggestState(state)
		}
	}

	m.transcript = append(m.transcript, transcriptEntry{
		role: "user", name: "tú", text: "crear tool para: " + cand.Pattern, ts: time.Now(),
	})

	prompt := fmt.Sprintf(
		"Cristalizá este patrón repetido en una tool nueva: %q (observado %d veces, la última vez el %s). "+
			"Llamá a tool_create con origin=\"agent\", reason describiendo la repetición detectada, "+
			"repetitions=%d, y sources con lo que uses para construir el pedido.",
		cand.Pattern, cand.N, cand.Last, cand.N,
	)
	user := convo.User(prompt)
	m.conv.Add(user)
	m = m.recordMessage(user)

	return m.startEngineTurn("")
}

// dismissSuggestion is "[n] no, ni ahora ni después": rule 2's "once per
// pattern, ever" (Ledger.DismissPattern), plus rule 4's decay counter
// (SuggestState.RecordRejection) — if the rejection streak just reached
// decayAfterRejects, EvolveStore.Decay() drops [tools.evolve].mode to
// "on_request" and a slashNotice announces it, the same "say so, once,
// on the transition" rule RecordRejection's own doc comment describes.
func (m Root) dismissSuggestion() (tea.Model, tea.Cmd) {
	cand := m.suggest.candidate
	m.suggest = suggestState{}
	m.mode = ModeChat

	if m.evolveStore == nil {
		return m, nil
	}

	if ledger, err := m.evolveStore.LoadLedger(); err == nil {
		ledger.DismissPattern(cand.Pattern)
		_ = m.evolveStore.SaveLedger(ledger)
	}

	state, err := m.evolveStore.LoadSuggestState()
	if err != nil {
		return m, nil
	}
	decayedNow := state.RecordRejection(m.decayAfterRejects)
	if err := m.evolveStore.SaveSuggestState(state); err != nil {
		return m, nil
	}
	if !decayedNow {
		return m, nil
	}
	if err := m.evolveStore.Decay(); err != nil {
		return m, nil
	}
	return m.slashNotice(m.lay.glyphs().warnMark +
		" varias sugerencias rechazadas seguidas: [tools.evolve].mode bajó a \"on_request\".")
}

func wrapSuggestLines(text string, width int) []string {
	return strings.Split(wrapText(text, width), "\n")
}

// renderSuggest draws the whole overlay: pattern summary, an optional
// detail block (toggled by "[v]"), the three selectable rows with the
// same pointer+highlight convention confirm.go/toolapprove.go already
// use, and a footer hint line. No lightbulb glyph — glyphs.go's own
// WGL4-restricted tables have nothing like it, and reportCompactDone's
// own "->" vs "→" precedent already settles for plain prose over
// inventing a one-off character for a single screen.
func (m Root) renderSuggest() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	s := m.suggest
	cand := s.candidate

	var b strings.Builder
	b.WriteString(" sugerencia: cristalizar un patrón repetido\n")
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	for _, line := range wrapSuggestLines(fmt.Sprintf("visto %d veces, la última el %s:", cand.N, cand.Last), width-1) {
		b.WriteString(" " + line + "\n")
	}
	for _, line := range wrapSuggestLines("  "+cand.Pattern, width-1) {
		b.WriteString(" " + line + "\n")
	}

	if s.detail {
		b.WriteString("\n")
		for _, line := range wrapSuggestLines(
			"todavía no hay nombre ni manifiesto propuesto: al elegir \"crearla\" se le pide al "+
				"modelo que llame a tool_create para este patrón, con la misma aprobación de "+
				"herramienta de siempre (danger: high, nunca delegable) antes de escribir nada.",
			width-1,
		) {
			b.WriteString(" " + line + "\n")
		}
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	for i, opt := range s.options() {
		pointer := " "
		if i == s.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + opt.label
		if i == s.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter elegir  esc ahora no\n", g.scrollHint)
	return b.String()
}
