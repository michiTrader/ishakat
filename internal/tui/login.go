// login.go implements Step 24's in-session half of /login (§13): the
// interactive counterpart to `ishakat login <provider>` (cmd/ishakat/login.go),
// reachable without leaving a running TUI session. It follows compact.go's
// own shape (compactState/startCompact/updateCompact/cancelCompact/
// finishCompact/renderCompact) as closely as the two async legs a login
// needs — a quick device-code request, then a slow poll-for-token wait —
// allow: two async results instead of ModeCompact's one, so there are two
// messages (loginCodeMsg, loginDoneMsg) instead of compactDoneMsg's single
// one, but the cancellation, staleness-guard and render-while-busy pattern
// are unchanged.
//
// Nothing in this file imports net/http or internal/oauth: the actual
// device-flow HTTP calls happen behind m.loginFor (loginfactory.go), the
// same §6.1 seam EngineFactory already draws for switching models.
// config.ProviderPreset/ResolveProviderPreset are safe to use directly —
// internal/config does not import net/http (confirmed via go list -deps,
// see docs/PLAN.md's Step 24 entries) — so this file resolves and
// validates the provider name itself, exactly like cmdLogin does, before
// ever calling m.loginFor.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
)

// loginState is ModeLogin's own state, live only while m.mode == ModeLogin —
// the same convention compactState already establishes.
type loginState struct {
	// preset is the provider being authenticated, resolved once by
	// startLogin so every later step (the device-code display, the
	// final success line) agrees on the same name.
	preset config.ProviderPreset

	// waiting is true once the device code is in hand and the wizard is
	// polling for the user to finish authorizing in a browser — the
	// dividing line renderLogin uses to pick which screen to draw.
	waiting bool

	// code is the device code/URL to show once RequestDeviceCode
	// returns; the zero value while still waiting on that first call.
	code LoginDeviceCode

	// waiter is what waitLoginCmd blocks on; nil until finishLoginCode
	// sets it.
	waiter LoginWaiter
}

// startLogin begins a /login attempt (§13, slashrun.go's KindLogin case).
// providerArg is whatever text followed "/login" (e.g. "openai"); "" means
// no provider was named, which is a usage error here exactly like
// cmdLogin's own "len(rest) == 0" branch — there is no picker for this,
// since ProviderPresets() is a short, fixed list a user is expected to
// name directly.
func (m Root) startLogin(providerArg string) (tea.Model, tea.Cmd) {
	if m.loginFor == nil {
		return m.slashNotice(m.lay.glyphs().warnMark +
			" /login todavia no esta disponible en esta build: usa `ishakat login <proveedor>` desde la terminal")
	}

	name := strings.TrimSpace(providerArg)
	if name == "" {
		return m.slashNotice(m.lay.glyphs().warnMark +
			" uso: /login <proveedor> (omniroute, openai, anthropic, nvidia, gemini)")
	}

	preset, err := config.ResolveProviderPreset(name)
	if err != nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " " + err.Error())
	}

	m.login = loginState{preset: preset}
	m.mode = ModeLogin
	m.animOffset = 0

	ctx, cancel := context.WithCancel(context.Background())
	m.loginCancel = cancel

	cmds := []tea.Cmd{requestLoginCodeCmd(ctx, m.loginFor, preset.ID)}
	if !m.lay.AnimationsOff {
		cmds = append(cmds, tickAnim(m.fps))
	}
	return m, tea.Batch(cmds...)
}

// requestLoginCodeCmd wraps m.loginFor's own RequestDeviceCode call as a
// tea.Cmd, the same wrapping summarizeCmd applies for engine.Summarize:
// Bubble Tea already runs every Cmd in its own goroutine, so blocking here
// is safe and needs no extra goroutine of this package's own.
func requestLoginCodeCmd(ctx context.Context, factory LoginFactory, providerID string) tea.Cmd {
	return func() tea.Msg {
		code, waiter, err := factory(ctx, providerID)
		return loginCodeMsg{code: code, waiter: waiter, err: err}
	}
}

// waitLoginCmd wraps LoginWaiter.Wait the same way, for the second, slower
// leg (the up-to-15-minute poll plus verify-then-write).
func waitLoginCmd(ctx context.Context, waiter LoginWaiter) tea.Cmd {
	return func() tea.Msg {
		note, err := waiter.Wait(ctx)
		return loginDoneMsg{note: note, err: err}
	}
}

// finishLoginCode is loginCodeMsg's handler: either the device code
// arrived (move to the "waiting" screen and kick off the poll) or the
// first call itself failed (close the wizard and report why).
func (m Root) finishLoginCode(code LoginDeviceCode, waiter LoginWaiter, err error) (tea.Model, tea.Cmd) {
	if err != nil {
		return m.cancelLoginWith(m.lay.glyphs().warnMark + " login fallido: " + err.Error())
	}
	m.login.code = code
	m.login.waiter = waiter
	m.login.waiting = true

	ctx, cancel := context.WithCancel(context.Background())
	m.loginCancel = cancel
	return m, waitLoginCmd(ctx, waiter)
}

// finishLogin is loginDoneMsg's handler: the poll-for-token-and-save
// sequence either produced a success line (note) or failed, mirroring
// runLogin's own final branch in cmd/ishakat/login.go.
//
// F2 (docs/ROADMAP-ux-2026-08-20.md's W4): a successful login just wrote a
// new credential to disk (config.SaveProviderConnection/SaveCredential,
// internal/app/loginfactory.go), but nothing about that write is visible
// to this running session yet — m.cat still shows whatever the catalog
// looked like at boot (or the last refresh), and the newly-authenticated
// provider has no way to appear in /model or ctrl+p without a hot refresh.
// Before this fix, closing the wizard was the last thing that ever
// happened here; the provider stayed unusable until a separate
// --refresh/restart, which is exactly the gap F2 names. When
// catalogRefreshFor is wired, the success path now also kicks off
// refreshCatalogCmd alongside cancelLoginWith's own cmd, batched together
// (tea.Batch) so cleanup is not delayed waiting on the network. The
// refresh's own result lands back through the ordinary CatalogRefreshedMsg
// path (applyCatalogRefreshed) once it completes, at which point the
// picker (if open) rebuilds and any live /model resolution can see the
// newly-usable provider — no restart, no --refresh, exactly what F2 asks
// for. A nil catalogRefreshFor (every test in this package, and any
// caller with nothing wired) falls back to the pre-F2 behaviour
// unchanged.
func (m Root) finishLogin(note string, err error) (tea.Model, tea.Cmd) {
	if err != nil {
		return m.cancelLoginWith(m.lay.glyphs().warnMark + " login fallido: " + err.Error())
	}
	next, cmd := m.cancelLoginWith(note)
	if m.catalogRefreshFor == nil {
		return next, cmd
	}
	refresh := refreshCatalogCmd(context.Background(), m.catalogRefreshFor)
	if cmd == nil {
		return next, refresh
	}
	return next, tea.Batch(cmd, refresh)
}

// updateLogin handles every message while mode == ModeLogin. Like
// updateCompact, only esc/ctrl+c do anything while the wizard is busy —
// loginCodeMsg/loginDoneMsg are handled one layer up, in updateDispatch.
func (m Root) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if keyPressString(key) == m.keys.Cancel {
			return m.cancelLogin()
		}
		return m, nil
	}
	return m, nil
}

// cancelLogin implements esc/ctrl+c while ModeLogin: cancels whichever
// network call is in flight and returns to ModeChat with a plain
// "cancelled" notice.
func (m Root) cancelLogin() (tea.Model, tea.Cmd) {
	return m.cancelLoginWith(m.lay.glyphs().warnMark + " login cancelado")
}

// cancelLoginWith is cancelLogin/finishLoginCode/finishLogin's shared
// close: stop the context, clear the state, return to ModeChat, and leave
// notice as a transcript entry (never part of m.conv — this is interface
// feedback, the same rule slashNotice's own doc comment states).
func (m Root) cancelLoginWith(notice string) (tea.Model, tea.Cmd) {
	if m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
	}
	m.login = loginState{}
	m.mode = ModeChat
	m.animOffset = 0
	return m.slashNotice(notice)
}

// renderLogin draws the wizard: the device code/URL once known, or a
// plain "requesting a device code..." spinner screen before that.
func (m Root) renderLogin() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()

	var b strings.Builder
	b.WriteString(" autenticando " + m.login.preset.Name + "\n")
	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")
	if !m.login.waiting {
		b.WriteString(" solicitando codigo de dispositivo...\n")
	} else {
		b.WriteString(" abre " + m.login.code.VerificationURI + " y escribe este codigo:\n\n")
		b.WriteString("     " + m.login.code.UserCode + "\n\n")
		b.WriteString(" esperando autorizacion...\n")
	}
	b.WriteString(" " + CrushFrame(m.lay, m.animOffset) + "\n")
	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")
	b.WriteString(" esc cancela\n")
	return b.String()
}
