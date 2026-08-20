package tui

import (
	"time"

	"github.com/MichiTrader/ishakat/internal/ask"
	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
)

// msgs.go concentra TODOS los tea.Msg propios de este paquete (§6.2): si hay
// que tocar dos archivos para agregar un mensaje nuevo, el diseño está mal.

// streamTickMsg drena el StreamBuf del turno vivo (§7.3). Se re-emite solo
// mientras hay un turno en curso: en reposo no hay ningún ticker de fondo.
type streamTickMsg struct{}

// animTickMsg avanza un fotograma del spinner / degradado en movimiento.
// Corre a ui.animations.fps y, como streamTickMsg, solo se re-emite en
// ModeBusy: la app en reposo consume 0% de CPU (§14).
type animTickMsg struct{ t time.Time }

// quitConfirmMsg fires if Quit is not pressed enough times inside the
// grace window: it clears quitPresses (§7.4, RC-1).
type quitConfirmMsg struct{}

// modelChosenMsg is the model picker's only output (§9.4/Step 10): the
// reference the user picked, or that /model resolved unambiguously without
// even opening the overlay. Routing the choice through a message instead of
// having the picker mutate Root directly keeps picker.go ignorant of
// anything but the catalog it was handed — the same separation slash.Kind
// already buys internal/slash from internal/tui.
type modelChosenMsg struct{ Ref string }

// sessionChosenMsg is the §13 /resume menu's only output: the ID of the
// session the user picked. Routing the choice through a message instead of
// having the menu mutate Root directly follows the same rule modelChosenMsg
// already applies for the picker — resumemenu.go stays ignorant of anything
// but the SessionSummary rows it was handed.
type sessionChosenMsg struct{ ID string }

// compactDoneMsg is startCompact's async result (§9.8/Step 12): the summary
// engine.Summarize produced, or the error that made compact_model's call
// fail — never both, engine.Summarize's own contract. Like modelChosenMsg it
// is a one-shot result rather than a repeating tick, so it needs no "does
// the timer stop" story: the goroutine that produces it runs exactly once
// per compaction, started by startCompact and never re-armed.
type compactDoneMsg struct {
	summary string
	err     error
}

// CatalogRefreshedMsg is the result of the §4.4/§11 background catalog
// refresh started once, right after the program is created (see app.Run):
// LoadCatalog only ever reads disk, so the interface is drawn immediately
// with whatever was cached, and this message is how the network's answer —
// discovery against every enabled provider, plus models.dev — reaches Root
// once it is ready, without blocking startup on it.
//
// It is exported (unlike every other message in this file) because the
// goroutine that produces it lives in internal/app, on the far side of the
// import boundary §6.1 draws between app and tui: app knows about
// *config.Config and the network, tui does not, so app.Run has to be the
// one calling RefreshCatalog and handing the result back across that
// boundary as a message on the *tea.Program it already holds.
//
// Catalog is nil when the refresh could not improve on what LoadCatalog
// already produced (see app.BackgroundRefresh) — applyCatalogRefreshed
// treats that as a no-op rather than replacing a good catalog with nothing.
type CatalogRefreshedMsg struct{ Catalog *catalog.Catalog }

// ToolApproveRequestMsg is how a permissions.Reviewer bridge running inside
// RunAgentTurn's goroutine (started by agentTurnCmd, a tea.Cmd — see
// agentturn.go) asks Update to open the ModeToolApprove overlay: the
// bridge's Review call is blocked, deep inside the agent loop, on Reply —
// the channel Update's eventual decision travels back on — and this
// message is the only way that goroutine can reach Root at all, the same
// role compactDoneMsg plays for summarizeCmd's own goroutine. It is not a
// one-shot *result* the way compactDoneMsg is: nothing here ends the
// turn, it only pauses it until resolveToolApproveWith sends a
// permissions.Decision down Reply.
//
// It is exported (unlike every other message in this file, but exactly
// like CatalogRefreshedMsg above and for the same reason) because the
// permissions.Reviewer implementation that produces it lives in
// internal/app, on the far side of the import boundary §6.1 draws: the
// Reviewer holds the *tea.Program app.Run built and calls p.Send with a
// value of this exact type from inside Guard.Authorize's call to Review,
// which only internal/app can construct since toolapprove.go/agentturn.go
// (internal/tui) never import a concrete Reviewer.
type ToolApproveRequestMsg struct {
	Req   permissions.Request
	Reply chan<- permissions.Decision
}

// AskUserRequestMsg is how an ask.Asker bridge running inside
// RunAgentTurn's goroutine (started by agentTurnCmd, a tea.Cmd — see
// agentturn.go) asks Update to open the ModeAskUser overlay: the bridge's
// Ask call is blocked, deep inside the agent loop's own ask_user tool
// call, on Reply — the channel Update's eventual ask.Answers travels back
// on — and this message is the only way that goroutine can reach Root at
// all, the identical role ToolApproveRequestMsg already plays for a
// paused permissions.Reviewer. It is not a one-shot *result* either: the
// turn is not over when this arrives, only paused, until
// resolveAskUserWith sends ask.Answers down Reply.
//
// It is exported (unlike every other message in this file, but exactly
// like CatalogRefreshedMsg/ToolApproveRequestMsg above and for the same
// reason) because the ask.Asker implementation that produces it lives in
// internal/app, on the far side of the import boundary §6.1 draws: the
// asker holds the *tea.Program app.Run built and calls p.Send with a
// value of this exact type from inside internal/tools' AskUser.Run,
// which only internal/app can construct since askuser.go/agentturn.go
// (internal/tui) never import a concrete Asker.
type AskUserRequestMsg struct {
	Form  ask.Form
	Reply chan<- ask.Answers
}

// PhaseWaitMsg is how the OnWait→TUI bridge (internal/app's
// tuiWaitNotifier) reports that engine.RunAgentTurn's own retry loop is
// about to sleep out a retryable handshake failure (a 429 with
// Retry-After) — see engine.AgentOptions.OnWait's own doc comment, which
// names this exact display ("§21.2's auto·wait 22s") as the intended
// consumer. Unlike ToolApproveRequestMsg/AskUserRequestMsg it carries no
// reply channel: OnWait is fire-and-forget — the loop resumes on its own
// once Wait elapses, there is nothing here for Update to answer, only a
// status line to update.
//
// It is exported for the same reason those two are: the bridge that
// produces it lives in internal/app, on the far side of the §6.1 import
// boundary, since only that package is allowed to hold both a
// *tea.Program and a concrete engine.AgentOptions.OnWait closure.
type PhaseWaitMsg struct {
	Wait time.Duration
}

// agentTurnDoneMsg is agentTurnCmd's result (see agentturn.go/root.go's
// startEngineTurn tools-enabled branch): engine.RunAgentTurn blocks with no
// per-token callback, so — like summarizeCmd — it is wrapped in a tea.Cmd
// and its one finished AgentResult reaches Update as this message.
type agentTurnDoneMsg struct {
	result engine.AgentResult
	err    error
}

// loginCodeMsg is startLogin's first async result (§13/Step 24,
// login.go): the outcome of the one quick network call m.loginFor makes
// up front (RFC 8628 §3.1's device authorization request) — the code and
// URL to show the user, a LoginWaiter to poll for the rest, or the error
// that made even that first call fail (an undeclared client_id, an
// unreachable device-code endpoint). Exactly one of (code/waiter, err) is
// meaningful, the same one-shot contract compactDoneMsg's own fields keep.
type loginCodeMsg struct {
	code   LoginDeviceCode
	waiter LoginWaiter
	err    error
}

// loginDoneMsg is waitLoginCmd's result: the outcome of LoginWaiter.Wait —
// the (up to 15-minute) poll for the token, followed by the same
// verify-then-write steps `ishakat login`'s CLI half performs. note is the
// success line to show (mirroring runLogin's own "Configured X via OAuth
// device flow." line) when err is nil.
type loginDoneMsg struct {
	note string
	err  error
}

// There is deliberately no blink message here.
//
// There used to be one, re-armed every 500 ms from Init for the lifetime of the
// process, and it flipped a boolean that no renderer ever read. Two things were
// wrong with that. The smaller one is the dead field. The larger one is that
// §14 asks for zero CPU activity at idle, and a program that wakes up twice a
// second forever does not have it — on the target platform that is a phone
// battery paying for a variable nobody looks at.
//
// Nothing replaces it because nothing has to: input.go sets
// SetVirtualCursor(false), so the text cursor is the terminal's own, and every
// terminal blinks its own cursor without being asked. Drawing a blink ourselves
// would mean fighting the hardware for it.
//
// The rule this file now keeps: a message type may only exist here if a timer
// that emits it can be shown to stop. streamTickMsg stops when the turn ends,
// animTickMsg when the mode leaves ModeBusy, quitConfirmMsg after one shot.
