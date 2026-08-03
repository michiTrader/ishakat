// session.go wires the TUI to session persistence (§10) — the half of it that
// this package is allowed to know about, which is deliberately very little.
//
// Why an interface instead of a *convo.Store: this package does not open
// files or decide where they live (see Options.CWD's own comment for the
// same rule applied to paths). internal/app already follows that rule for
// every other resolved value it hands over — CWD is pre-prettified, Cap and
// Glyphs are pre-detected, the catalog is loaded from disk before NewRoot is
// called — and persistence is the one that would have broken it most
// quietly: opening a file is one line, and the failure would not show up as
// a build error, only as this package quietly knowing what XDG is.
//
// So the TUI gets a Recorder: something that accepts completed messages and
// might fail. Where those bytes land, whether the directory had to be
// created, what happens when the disk is full — none of that is answerable
// here, and none of it needs to be.
package tui

import "github.com/MichiTrader/ishakat/internal/convo"

// Recorder is where completed messages go to be remembered. internal/app
// implements it over convo.Store; tests implement it in a few lines to
// assert what was recorded and in what order.
//
// The contract has exactly one rule and it is §10's: Append is called with a
// message that is *finished*. Never during streaming. A JSONL file whose
// last line is half a token is not a file that survives `kill -9`, which is
// the entire reason the format was chosen over SQLite (§3).
type Recorder interface {
	// Append persists one completed message. An error means this message
	// was not saved; it does not mean the recorder is dead, and the caller
	// is expected to keep going either way — see Root.recordMessage.
	Append(m convo.Message) error
}

// recordMessage persists one completed message, if there is anywhere to
// persist it to, and reports the failure to the user exactly once.
//
// A persistence failure must never cost the user their turn. The response
// is already on screen and already paid for; refusing to continue because a
// file could not be written would destroy something valuable to protect
// something replaceable. This mirrors headless.go's own rule ("losing the
// file is annoying; losing the response the user already paid for is not
// acceptable") — and it matters more here, because in the TUI the
// alternative is not a warning on stderr but a dead interface holding a
// conversation the user can still read and copy on screen.
//
// The failure is reported once and then suppressed: a full disk fails on
// every single message, and a notice per message would bury the transcript
// under identical errors — turning a recoverable annoyance into an unusable
// screen. sessionWarned is what makes it exactly once.
func (m Root) recordMessage(msg convo.Message) Root {
	if m.recorder == nil {
		return m // [session] save = false, or the store never opened.
	}
	if err := m.recorder.Append(msg); err != nil && !m.sessionWarned {
		m.sessionWarned = true
		m.sessionErr = err
	}
	return m
}
