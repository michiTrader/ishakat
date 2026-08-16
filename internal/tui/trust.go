// trust.go implements §21.4 layer 2's first-run trust dialog (Step 30):
// "on the first interactive run in a directory that has no saved decision,
// exactly one question — and then not again for days." NewRoot opens
// ModeTrust in place of ModeChat exactly when Options.NeedsTrust is true
// (internal/app already looked up internal/trust.Store and found no record
// covering this project's path, or any ancestor); every other caller
// (already-trusted project, or any test in this package) never builds this
// state at all, the same "do not build state nobody asked for" rule
// themePicker/confirm already follow for their own overlays.
//
// Like themepicker.go this is a flat, hand-rolled selection list rather than
// an ask.State/ask.Form: there is exactly one question here, asked exactly
// once at startup, so ask.Form's tab bar and multi-question machinery would
// be pure overhead. Option 4 ("Type something...") is accepted as a row —
// §21.4's own mockup draws it — but, for this pass, choosing it resolves to
// the same "Ask before changes" (Agile) autonomy Esc already defaults to
// rather than opening a free-text widget: Step 30's own closing criterion
// ("second run in a known project asks nothing") only needs *some* decision
// recorded, and a bespoke textinput.Model sub-widget for a string this
// package would not otherwise know how to interpret is left for a later
// step (docs/PLAN.md's own §21.4 layer 5 "Rule" territory) rather than
// invented here half-finished.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// TrustStore is §21.4 layer 2's own persistence seam — the same shape
// ThemeStore (theme.go) already draws for /theme's own write, and the same
// "nil is a supported value" contract: a nil TrustStore still applies the
// chosen autonomy for the running session (resolveTrust sets m.footer.Autonomy
// regardless), it just does not survive a restart. internal/app is expected
// to implement this over internal/trust.Store.Set + internal/trust.Save,
// capturing the project's own path by closure — mirroring
// internal/app/themestore.go's fileThemeStore over config.SetTheme — so this
// package never has to know a path is part of the persisted record at all.
type TrustStore interface {
	Save(autonomy string) error
}

// trustOption is one selectable row of the dialog, in the exact order
// §21.4's own mockup lists them.
type trustOption struct {
	label       string
	description string
	autonomy    string // permissions.Autonomy.String()'s own vocabulary: "auto", "agile", "readonly"
}

// trustOptions is §21.4's own four rows, in its own order. Option 4's
// autonomy resolves to "agile" — see this file's package comment for why a
// free-text widget is not implemented in this pass.
var trustOptions = []trustOption{
	{label: "1. Auto", description: "Explore, run safe commands and edit files inside the project.", autonomy: "auto"},
	{label: "2. Ask before changes", description: "Same, but confirm every write.", autonomy: "agile"},
	{label: "3. Read only", description: "Never writes. For auditing.", autonomy: "readonly"},
	{label: "4. Type something...", description: "", autonomy: "agile"},
}

// trustDialogDefault is the row Esc resolves to — §21.4's own "Esc defaults
// to the safer option, never the recommended one" rule. Index 1 is
// "2. Ask before changes" (Agile), never index 0 ("1. Auto").
const trustDialogDefault = 1

// trustDialog is ModeTrust's own state, live only while mode == ModeTrust.
type trustDialog struct {
	path      string // xdg.Pretty'd already, by whoever set Options.CWD
	gitInGit  bool
	gitClean  bool
	gitBranch string
	sel       int
}

// newTrustDialog builds the dialog. The cursor starts on option 1 ("Auto"),
// exactly where §21.4's own mockup draws it (the ">" pointer sits on row 1)
// — recommending it visually is not the same as choosing it for the human,
// which is exactly why Esc still resolves to row 2 regardless of where the
// cursor has moved.
func newTrustDialog(cwd string, gitInGit, gitClean bool, gitBranch string) trustDialog {
	return trustDialog{path: cwd, gitInGit: gitInGit, gitClean: gitClean, gitBranch: gitBranch, sel: 0}
}

// moveSel moves the selection by delta rows, wrapping like every other
// dialog's own moveSel (themepicker.go, confirm.go).
func (d trustDialog) moveSel(delta int) trustDialog {
	n := len(trustOptions)
	d.sel = ((d.sel+delta)%n + n) % n
	return d
}

// updateTrust handles every key while mode == ModeTrust. Like
// updateThemePicker it owns the keyboard outright — there is no textarea
// underneath it, and unlike every other overlay in this package, Cancel does
// not simply discard and return unchanged: a first-run project must always
// leave this dialog with *some* trust decision recorded (§21.4's own
// "Esc = 2" rule), never back in an undecided state.
func (m Root) updateTrust(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.resolveTrust(trustOptions[trustDialogDefault].autonomy)
	case "up":
		m.trust = m.trust.moveSel(-1)
		return m, nil
	case "down":
		m.trust = m.trust.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveTrust(trustOptions[m.trust.sel].autonomy)
	}
	return m, nil
}

// resolveTrust applies autonomy immediately (m.footer.Autonomy, drawn by the
// status line since PR #144) and persists it best-effort via
// m.trustStore.Save — the exact same "the display already changed, hiding a
// write failure would be a worse surprise" reasoning switchTheme (theme.go)
// already follows for its own store write. It always returns to ModeChat:
// there is no turn in flight yet for this dialog to pause, the same
// one-way-close reasoning ModeThemePicker's own doc comment gives.
func (m Root) resolveTrust(autonomy string) (tea.Model, tea.Cmd) {
	m.trust = trustDialog{}
	m.mode = ModeChat
	m.footer.Autonomy = autonomy

	g := m.lay.glyphs()
	msg := g.assistantMark + " modo: " + autonomy
	if m.trustStore != nil {
		if err := m.trustStore.Save(autonomy); err != nil {
			msg += " (no se pudo guardar: " + err.Error() + ")"
		}
	}
	return m.slashNotice(msg)
}

// renderTrust draws §21.4's own mockup verbatim (project path, git line,
// question, four options, help line). Like renderThemePicker/renderConfirm
// it replaces the whole live region and draws no box border — this
// package's glyph table has none, and inventing one for a single screen is
// exactly what glyphs.go's own comment warns against.
func (m Root) renderTrust() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.trust

	var b strings.Builder
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	b.WriteString(" New project\n")
	fmt.Fprintf(&b, " %s\n", ShortenPath(d.path, width-1))
	b.WriteString(" " + trustGitLine(d) + "\n")
	b.WriteString("\n How should I work here?\n\n")

	for i, opt := range trustOptions {
		pointer := " "
		if i == d.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + opt.label
		if i == d.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
		if opt.description != "" {
			b.WriteString("      " + opt.description + "\n")
		}
	}

	b.WriteString("\n " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter choose  esc = 2\n", g.scrollHint)
	return b.String()
}

// trustGitLine renders §21.4's own "git: yes · clean · branch main" line
// (or the "git: no" degradation for an ordinary non-git directory — the
// zero value for a caller that never calls internal/app.DetectGit).
func trustGitLine(d trustDialog) string {
	if !d.gitInGit {
		return "git: no"
	}
	state := "dirty"
	if d.gitClean {
		state = "clean"
	}
	branch := d.gitBranch
	if branch == "" {
		branch = "?"
	}
	return fmt.Sprintf("git: yes · %s · branch %s", state, branch)
}
