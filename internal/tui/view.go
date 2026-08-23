package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichiTrader/ishakat/internal/termenv"
)

// View satisface tea.Model. En v2 View() devuelve tea.View, no un string
// (§7.2).
//
// This is docs/DESIGN-tui-mode.md §4 Rule 2's split, made literal:
//
//	render(state, width, height) -> Frame     // shared. no mode awareness.
//	emit(Frame, mode)            -> tea.View  // the ONLY mode-aware function.
//
// View itself does neither: it calls render() to get the mode-blind Frame,
// then hands that Frame plus m.tuiMode to emit, which is the one function in
// this package allowed to look at the mode at all. That is what makes the
// eventual fullscreen behaviour (owning AltScreen, its own scrollback, its
// own resize repair) a change confined to emit's fullscreen branch, instead
// of a change that has to go hunt down every place mode-awareness leaked
// into — which is exactly the "two frágil renderers" the owner's constraint
// (quoted in §4) rules out.
func (m Root) View() tea.View {
	return emit(Frame{Content: m.render()}, m.tuiMode, m.cursorFor())
}

// render arma la región viva completa: banner (solo al arranque, cuando no
// hay transcript todavía), transcript comprometido, turno vivo si lo hay, y
// la caja de entrada con el footer. Frame's Width mirrors m.lay.Width so
// emit can re-derive AltScreen's window without importing Layout itself
// (see Frame's own doc comment).
//
// This function, and everything it calls (renderRaw, head, clampFrameWidth,
// fold, and every layout helper in footer.go/input.go/etc.), is Rule 2's
// "lives above the seam" half: none of them take or read a termenv.Mode.
// grep confirms this rather than asserting it — m.tuiMode has exactly two
// readers in the whole package as of this commit: View (above) and
// debugcmd.go's /debug line, neither of which is on this call path.
func (m Root) render() string {
	return clampFrameWidth(m.fold(m.renderRaw()), m.lay.Width)
}

// Frame is render's mode-blind output: what the frame looks like, with
// nothing yet said about how — or whether — it reaches the real terminal.
// It is a thin wrapper around a string today rather than a cell grid,
// deliberately: Rule 2 only requires that render and emit be two functions
// with no mode leaking into the first one, not that the shared type be
// richer than what render already produces. Growing Frame into something
// emit's fullscreen branch can repaint cell-by-cell (rather than re-run
// render() wholesale on every resize, which is what Rule 3 already asks for
// and is sufficient for a first cut) is deferred until that branch actually
// needs it — introducing the richer shape before there is a second
// consumer of it would be exactly the kind of speculative structure §4
// warns against.
type Frame struct {
	// Content is render's output: the width-clamped, glyph-folded string
	// View() used to pass to v.SetContent directly.
	Content string
}

// emit is Rule 2's only mode-aware function. It takes the Frame render()
// built with no knowledge of the mode, and decides how it reaches the real
// terminal.
//
//   - regular: unchanged from before this split — AltScreen stays false, so
//     Bubble Tea's inline renderer keeps the terminal's own scrollback and
//     text selection working, and evictOverflow (root.go)/commitEntryCmd
//     (chat.go) go on being what permanently commits a line via
//     tea.Println. That is "printed means final" (§4, regular's own rule),
//     and none of it changes here.
//   - fullscreen: AltScreen becomes true — Bubble Tea's own alternate
//     screen, with its own buffer the terminal restores on exit. This
//     package needs no richer Frame type or cell-grid viewport to make
//     that correct for Rule 3 ("fullscreen repairs everything, because it
//     owns every visible cell"): render() (above) has no memoization
//     anywhere in this package — every View() call re-derives the whole
//     Frame from m.transcript/m.live/etc. from scratch — so the existing,
//     unconditional "rebuild from state" already *is* the repair strategy;
//     there is nothing a richer Frame would add that a fresh render()
//     does not already give for free on every message, including a
//     resize. What fullscreen actually changes elsewhere: evictOverflow
//     (root.go) no-ops in this mode, because its tea.Println eviction
//     mechanism has no valid destination once AltScreen is true — see its
//     own doc comment. The exit-transcript half of DECISION-1b is
//     deliberately out of scope for this function: Options.
//     FullscreenExitTranscript's own doc comment (root.go) explains why
//     that has to be handled entirely outside this package's Update/View
//     loop, by Root.ExitTranscript (below) and internal/app.Run, not by
//     anything emit or View could safely do here.
func emit(f Frame, mode termenv.Mode, cursor *tea.Cursor) tea.View {
	var v tea.View
	v.SetContent(f.Content)
	v.MouseMode = tea.MouseModeNone
	v.Cursor = cursor
	v.AltScreen = mode == termenv.ModeFullscreen
	return v
}

// ExitTranscript is DECISION-1b's exit-transcript flush, for a caller
// leaving fullscreen mode. It renders the *entire* transcript — every
// entry, not just transcript[printedUpTo:] the way headContent draws the
// live region, because printedUpTo never advances in fullscreen at all
// (evictOverflow's own fullscreen guard, root.go) — at the frame's own
// content width, ready to be written straight to the real terminal with a
// plain fmt.Print.
//
// This is deliberately a plain method, not a tea.Cmd and not something
// wired into Update/View: see Options.FullscreenExitTranscript's own doc
// comment (root.go) for the full reasoning, in short, bubbletea v2's
// renderer flushes bytes to the wire on an independent ticker, decoupled
// from the Update/View cycle any Cmd runs inside, so any attempt to
// sequence "print the transcript, then quit" from inside a running
// tea.Program is provably racy — the print can land on either side of the
// real AltScreen-exit write. The one deterministic point is Program.Run
// itself returning, which is guaranteed (by Program.shutdown ->
// stopRenderer -> renderer.close(), read from charm.land/bubbletea/v2's
// own source) to happen strictly after the real terminal is already back
// on the main screen. internal/app.Run is expected to call this on the
// final tea.Model p.Run() itself returns (type-asserted to Root), after
// p.Run() has already returned, and to fmt.Print the result directly —
// never through tea.Println, since by then there is no running Program
// left to hand a Cmd to.
//
// Returns "" — nothing to print — in every case that is not "fullscreen,
// with the feature enabled, with an actual terminal size known, and with
// at least one entry in the transcript": leaving regular mode never needed
// this in the first place (its scrollback was always real), a Root that
// never saw a WindowSizeMsg has no width to wrap against, and an empty
// transcript has nothing worth printing.
func (m Root) ExitTranscript() string {
	if m.tuiMode != termenv.ModeFullscreen || !m.exitTranscript {
		return ""
	}
	if m.lay.Height <= 0 {
		return ""
	}
	if len(m.transcript) == 0 {
		return ""
	}
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	var b strings.Builder
	for _, e := range m.transcript {
		b.WriteString(renderTranscriptLine(m.styles, g, width, e.role, e.name, e.text, e.ts, m.cfgSyntax, m.cfgMarkdown, m.foldCode, e.reasoning, m.cfgReasoning))
		b.WriteString("\n\n")
	}
	return b.String()
}

// clampFrameWidth is RC-5's width invariant: no line render() returns is ever
// wider than the terminal, full stop. It sits on render's own choke point,
// the same way fold does, for the same reason: a rule enforced once here can
// be checked, and a rule that depends on every component (wrapText, the
// footer, the banner, the spinner strip, …) getting its own width budget
// exactly right is a rule that only has to be wrong once.
//
// headRows/frameRows — and, one level up, evictOverflow's and cursorFor's
// whole "N newlines is N rows" bookkeeping — only hold if this is true. A
// single line wider than the terminal gets auto-wrapped by the terminal
// itself into two or more real rows while this package's own count still
// treats it as one, which is the "row accounting drifting" RC-5 names as the
// actual cause behind B4's duplicated, six-times-printed banner: the
// renderer's next up-move lands mid-frame and repaints over live text
// instead of replacing it.
func clampFrameWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// fold is the single point where a restricted terminal gets a string it can
// actually represent. It sits on the way out of render rather than inside each
// component for the reason the glyph table exists at all: a rule applied at one
// choke point can be checked, and a rule applied in six render functions is a
// rule that will be forgotten in the seventh.
//
// Escape sequences are ASCII, so folding the finished frame does not touch the
// colours the styles put in.
func (m Root) fold(s string) string {
	if !m.lay.ASCII() {
		return s
	}
	return foldASCII(s)
}

func (m Root) renderRaw() string {
	if m.mode == ModeHelp {
		return m.renderHelp()
	}
	if m.mode == ModeHotkeys {
		return m.renderHotkeys()
	}
	if m.mode == ModePicker {
		return m.renderPicker()
	}
	if m.mode == ModeConfirm {
		return m.renderConfirm()
	}
	if m.mode == ModeCompact {
		return m.renderCompact()
	}
	if m.mode == ModeResume {
		return m.renderResumeMenu()
	}
	if m.mode == ModeToolApprove {
		return m.renderToolApprove()
	}
	if m.mode == ModeAskUser {
		return m.renderAskUser()
	}
	if m.mode == ModeLogin {
		return m.renderLogin()
	}
	if m.mode == ModeSuggest {
		return m.renderSuggest()
	}
	if m.mode == ModeThemePicker {
		return m.renderThemePicker()
	}
	if m.mode == ModeTrust {
		return m.renderTrust()
	}
	if m.mode == ModeMission {
		return m.renderMission()
	}
	if m.mode == ModeToolScope {
		return m.renderToolScope()
	}
	if m.mode == ModeQueueEdit {
		return m.renderQueueEdit()
	}

	var b strings.Builder
	b.WriteString(m.head())
	if menu := m.slashMenuBlock(); menu != "" {
		b.WriteString(menu)
	}
	if menu := m.atMenuBlock(); menu != "" {
		b.WriteString(menu)
	}
	b.WriteString(InputBox(m.lay, m.styles, m.input.View()))
	b.WriteString("\n")
	b.WriteString(RenderFooter(m.lay, m.footerState(), m.footerItems))

	return b.String()
}

// slashMenuBlock is the §9.6 dropdown as drawn directly above the input box,
// or "" when it has nothing to show. It is a method of its own — like
// head() — because render and cursorFor must agree on its height down to the
// row.
func (m Root) slashMenuBlock() string {
	if !m.menu.Active() {
		return ""
	}
	return renderSlashMenu(m.lay, m.styles, m.menu) + "\n"
}

// atMenuBlock is F18's own "@" path-completion dropdown as drawn directly
// above the input box (atmenu.go) — the exact structural sibling of
// slashMenuBlock above, kept as its own method for the identical reason:
// render and cursorFor must agree on its height down to the row. By
// construction at most one of slashMenuBlock/atMenuBlock is ever non-""
// for the same Root (see updateChat's own comment on why m.menu and
// m.atMenu never hold simultaneously), so the two blocks never stack.
func (m Root) atMenuBlock() string {
	if !m.atMenu.Active() {
		return ""
	}
	return renderAtMenu(m.lay, m.styles, m.atMenu) + "\n"
}

// head is everything drawn above the input box: the start-up banner (only
// while there is nothing in the transcript), the committed transcript and the
// live turn — clipped to headBudget() when it does not fit (RC-3's height
// invariant).
//
// It is a method of its own because render and cursorFor must agree on its
// height down to the row. Measuring one thing and drawing another is precisely
// how the cursor ended up next to the banner. That is also why the clip has
// to live inside this method rather than only in renderRaw: cursorFor calls
// head() directly (see its own comment), and if it saw the unclipped content
// while renderRaw drew the clipped one, the two would disagree again — the
// exact class of bug this method's own doc comment already warns about.
func (m Root) head() string {
	if m.lay.Height <= 0 {
		// No terminal size known yet (a Root that has never seen a
		// WindowSizeMsg): there is nothing to clip against, and clipping
		// blind would just be a second way to get the arithmetic wrong.
		return m.headContent()
	}
	return clipHead(m.headContent(), m.lay.glyphs(), m.headBudget())
}

// headContent builds head()'s full, unclipped content: the start-up banner,
// every transcript entry evictOverflow has not yet retired to scrollback,
// and the live turn.
//
// It exists separately from head() so evictOverflow can measure how tall the
// *unclipped* region actually is (frameRowsUnclipped) without that
// measurement being contaminated by head()'s own clip — eviction is the
// preferred way to make room (it is permanent: the entry leaves the live
// region for good), and clipHead is only the backstop for what eviction's
// own keepInline floor and a still-growing live turn cannot reach. Measuring
// the clipped string here would make evictOverflow see a frame that always
// already fits and stop evicting anything, which defeats the point of
// having both mechanisms.
func (m Root) headContent() string {
	g := m.lay.glyphs()
	var b strings.Builder

	if banner := m.bannerText(); banner != "" {
		b.WriteString(banner)
		b.WriteString("\n\n")
	}

	// Only entries not yet handed to commitEntryCmd are redrawn here.
	// Printed ones already live in the terminal's real scrollback (§7.5);
	// keeping them here too would be drawing the same line twice and, past a
	// certain history length, is exactly what grew the live region past the
	// terminal's height (see commitEntryCmd's comment).
	width := m.lay.ContentWidth()
	for _, e := range m.transcript[m.printedUpTo:] {
		b.WriteString(renderTranscriptLine(m.styles, g, width, e.role, e.name, e.text, e.ts, m.cfgSyntax, m.cfgMarkdown, m.foldCode, e.reasoning, m.cfgReasoning))
		b.WriteString("\n\n")
	}

	if m.live.active {
		b.WriteString(renderLiveTurn(m.styles, g, width, m.live, CrushFrame(m.lay, m.animOffset), " esc cancela\n", m.cfgSyntax, m.cfgMarkdown, m.foldCode, m.cfgReasoning))
		b.WriteString("\n")
	}

	return b.String()
}

// headRows is how many terminal rows head occupies. Every block head writes
// ends with a newline, so the number of rows above the input box is exactly
// the number of newlines. Bubble Tea's inline renderer clips content to the
// terminal width instead of wrapping it, so a long line still costs one row
// and no wrap arithmetic is needed here.
func headRows(head string) int { return strings.Count(head, "\n") }

// restRows is every row render() draws *below* head(): the slash-menu
// dropdown (if open), the input box, and the footer. head() and
// evictOverflow both need "how much room is left once the rest of the frame
// has taken its share" — this is the one place that arithmetic is done, so
// headBudget (below) and evictOverflow's own budget check can never drift
// apart on what "the rest of the frame" means.
func (m Root) restRows() int {
	inputBoxRows := strings.Count(InputBox(m.lay, m.styles, m.input.View()), "\n") + 1
	footerRows := strings.Count(RenderFooter(m.lay, m.footerState(), m.footerItems), "\n") + 1
	return headRows(m.slashMenuBlock()) + headRows(m.atMenuBlock()) + inputBoxRows + footerRows
}

// headBudget is the most rows head() is allowed to occupy: frameBudget
// (root.go — the terminal's height, minus F20's one row of breathing room)
// minus whatever the rest of the frame already spends. Never negative: a
// terminal too short even for the input box and footer alone is a separate,
// pre-existing problem (ShowBoxedInput/ShowBorders already degrade at
// BPMinimo for exactly this reason) that clipping the empty head to 0 rows
// cannot make any worse.
func (m Root) headBudget() int {
	budget := m.frameBudget() - m.restRows()
	if budget < 0 {
		budget = 0
	}
	return budget
}

// frameRowsUnclipped is how tall the live-managed region would be with no
// clipping at all — headContent()'s real height plus everything below it.
// evictOverflow measures against this, not against render()'s own (already
// clipped) output, for the reason headContent's doc comment gives: eviction
// has to see the overflow head() is about to hide, or it would never run.
func (m Root) frameRowsUnclipped() int {
	return headRows(m.headContent()) + m.restRows()
}

// clipHead is RC-3's actual height invariant, applied to head(): when raw is
// taller than budget rows, the oldest rows (the top of head — the earliest
// still-inline transcript entry, or the earliest lines of a live turn once
// nothing else is left to drop) are hidden behind one "…N rows above" line
// rather than left to overflow the terminal.
//
// This is deliberately a *visual* clip, not a second eviction path: the rows
// it hides are still exactly where they were — in m.transcript, or in
// m.live's own growing text — and reappear the moment something else
// (another turn finishing, the window growing) gives them room again. That
// is the difference from evictOverflow: evictOverflow's rows are gone for
// good (printed to real scrollback); clipHead's are merely not drawn this
// frame.
//
// budget <= 0 means there is no room for anything, not even the affordance
// line — returns "" rather than a lone clip line that would itself overflow.
func clipHead(raw string, g glyphs, budget int) string {
	if budget <= 0 {
		return ""
	}
	rows := headRows(raw)
	if rows <= budget {
		return raw
	}
	// raw always ends in "\n" (every block headContent writes ends with
	// one), so splitting on it yields exactly rows+1 elements, the last of
	// which is the empty string after that final newline.
	lines := strings.Split(raw, "\n")
	content := lines[:len(lines)-1]
	keep := budget - 1 // one row of the budget is spent on the affordance itself
	if keep < 0 {
		keep = 0
	}
	hidden := len(content) - keep
	tail := content[len(content)-keep:]

	out := make([]string, 0, budget)
	unit := "row"
	if hidden != 1 {
		unit = "rows"
	}
	out = append(out, fmt.Sprintf("%s %d %s above", g.clipMark, hidden, unit))
	out = append(out, tail...)
	return strings.Join(out, "\n") + "\n"
}

// bannerText is the startup banner's rendered form, or "" once it should no
// longer be part of the live-managed region — the same condition head() used
// to check inline before submit (see its own comment) also needed this exact
// string, to hand to tea.Println instead of to head() on the one frame the
// condition flips from true to false.
func (m Root) bannerText() string {
	if len(m.transcript) != 0 || m.live.active {
		return ""
	}
	return Banner(m.lay, m.styles, m.version, m.bannerPath(), m.footer.Model, m.cfgBanner, m.animOffset)
}

// bannerPath is the working directory as the banner shows it: the whole path,
// abbreviated only as much as the terminal width forces.
func (m Root) bannerPath() string {
	// "ishakat " + version + " · " is everything the line spends before the
	// path, so that is exactly what the path budget has to give up. The
	// separator is measured rather than assumed: it is a glyph now, so its
	// width is a property of the terminal, and len() would have counted the
	// two bytes of "·" as two columns.
	spent := lipglossWidth("ishakat  "+m.lay.glyphs().dot+" ") + lipglossWidth(m.version)
	return ShortenPath(m.cwd, m.lay.ContentWidth()-spent)
}

// footerCWDShare is the fraction of the footer the path is allowed to take.
// The footer already drops items right to left when it overflows, but a full
// path would starve the model name — the one item nobody wants to lose —
// before the dropping logic ever got a chance to run.
const footerCWDShare = 3

// footerMinCWD is the floor of that share: below six columns the path becomes
// a single letter plus an ellipsis, which is noise rather than information.
const footerMinCWD = 6

// footerState fills in the parts of the footer that depend on the current
// width, leaving everything else as the model holds it.
func (m Root) footerState() FooterState {
	st := m.footer
	budget := m.lay.ContentWidth() / footerCWDShare
	if budget < footerMinCWD {
		budget = footerMinCWD
	}
	st.CWD = ShortenPath(m.cwd, budget)
	return st
}

// cursorFor is where the terminal's real cursor should sit: inside the
// textarea when the chat input box is on screen, or nil when an overlay
// has replaced that box (ModeHelp, ModePicker, and the rest of renderRaw's
// dedicated branches).
//
// ModeBusy still draws the input box (renderRaw's default path) even though
// keystrokes are swallowed (updateBusy). Returning nil here was RC-2: Bubble
// Tea v2 then leaves the hardware cursor wherever the last write ended,
// which is the box's bottom border — the reported └──❚────┘, and the reason
// the cursor appeared to be "taken away" the moment a task started. Showing
// the cursor in the empty input is not typing-while-busy (that is W2); it is
// only not taking the cursor away.
//
// textarea.Cursor() reports a position relative to the widget's own top-left
// corner, which the widget documents and which is easy to miss: returning it
// untouched puts the cursor at row 0 of the whole view — right next to the
// banner — instead of inside the input box. The offset added here is the box
// origin plus every row drawn above it.
func (m Root) cursorFor() *tea.Cursor {
	switch m.mode {
	case ModeChat, ModeBusy:
		// The chat input box is on screen. ModeBusy is included on
		// purpose (RC-2); do not add overlay modes here.
	default:
		return nil
	}
	c := m.input.Cursor()
	if c == nil {
		return nil
	}
	dx, dy := InputOrigin(m.lay)
	c.Position.X += dx
	c.Position.Y += dy + headRows(m.head()) + headRows(m.slashMenuBlock()) + headRows(m.atMenuBlock())
	return c
}

// renderHelp draws the §9.7 help screen. The command list is generated from
// m.commands (internal/slash.Registry.HelpLines) rather than hand-written:
// Step 9 closes the gap this function's comment used to document — the
// dropdown (slashMenuBlock) reads the very same table.
//
// F14 (roadmap W3): the two rule headings used to be padded to a fixed 38
// columns regardless of the terminal, which is the same "picked a width once
// and stopped looking" bug F14 names for the footer before this. width is
// measured once, from m.lay.ContentWidth() — the same call every other
// overlay (renderPicker, renderConfirm, renderCompact, renderResumeMenu,
// renderToolApprove, renderAskUser, renderThemePicker) already makes for its
// own rule lines — and handed to both headings so they keep sharing one
// width (TestHelpHeadingsShareOneWidth) without that width being a literal.
func (m Root) renderHelp() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	var b strings.Builder
	b.WriteString(helpHeading(g, width, "ishakat "+g.dot+" comandos") + "\n\n")
	for _, line := range m.commands.HelpLines() {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString("\n" + helpHeading(g, width, "atajos") + "\n\n")
	for _, line := range m.helpShortcuts() {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(fmt.Sprintf("\n %s desplazar %s esc volver", g.scrollHint, g.dot))
	return b.String()
}

// helpShortcuts builds the shortcut list from the loaded Map (RC-1).
// Hardcoded chords drifted from defaults.toml once already — quit was
// advertised as "ctrl+c×2" while the shipped binding was a two-word string
// that could never match. Generating the list from the same Map handleGlobalKey
// compares against is the audit the report asked for.
func (m Root) helpShortcuts() []string {
	k := m.keys
	if k.Quit == "" {
		k = defaultMap
	}
	quit := k.Quit
	if k.QuitRepeat > 1 {
		quit = fmt.Sprintf("%s×%d", k.Quit, k.QuitRepeat)
	}
	return []string{
		padHelpChord(k.ModelPicker) + "selector de modelos",
		padHelpChord(k.ModelCycle) + "rotar favoritos",
		padHelpChord(k.ThemePicker) + "selector de temas",
		padHelpChord(k.Newline) + "salto de línea",
		padHelpChord(k.Cancel) + "cancelar generación",
		padHelpChord(quit) + "salir",
		padHelpChord(k.ClearScreen) + "limpiar pantalla",
		padHelpChord(k.CopyLast) + "copiar última respuesta",
	}
}

// helpChordWidth is the column the descriptions start at. "ctrl+c×2" is 8
// runes; a space after the padded chord keeps the list aligned the way the
// hand-written lines used to be.
const helpChordWidth = 9

func padHelpChord(chord string) string {
	if n := helpChordWidth - len([]rune(chord)); n > 0 {
		return chord + strings.Repeat(" ", n)
	}
	return chord + " "
}

// helpHeading draws a section heading padded out to width with the rule
// glyph. width is renderHelp's m.lay.ContentWidth(), not a literal: see
// renderHelp's own comment for why (F14, roadmap W3).
//
// The headings used to be literal runs of U+2500 counted out by hand, which had
// two problems: the two of them came out different lengths (visible on screen,
// and impossible to keep aligned when either title is edited), and the box
// drawing character is one more thing a legacy console renders as garbage.
// Padding to a shared width fixed that; padding to a *fixed* 38 columns
// regardless of the terminal was the next thing wrong with it — the help
// screen stayed the same width whether the terminal was 40 columns (where it
// used to overflow, silently clipped by clampFrameWidth) or 200 (where it
// left most of the screen bare instead of using it, exactly the "does not
// reflow" half of F14 the roadmap calls out by name for this literal).
func helpHeading(g glyphs, width int, title string) string {
	const lead = 2
	prefix := strings.Repeat(g.rule, lead) + " " + title + " "
	if fill := width - lipglossWidth(prefix); fill > 0 {
		return prefix + strings.Repeat(g.rule, fill)
	}
	return prefix
}
