// session.go wires the TUI's persistence (§10, Step 13) over convo.Store.
// This is the only place allowed to know both what a session file is and
// what tui.Recorder expects — app.go's usual role of cabling config into
// something the TUI can hold without importing convo/xdg itself.
package app

import (
	"fmt"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// sessionRecorder adapts *convo.Store to tui.Recorder.
//
// The file is created lazily, on the first Append, rather than eagerly at
// Run: at NewRoot time nothing has been typed yet, so there is no title to
// give convo.Store.New the way headless.go's openSession has one immediately
// (the whole prompt is already on the command line there). Waiting for the
// first message is not a workaround, it is the same rule headless follows —
// title comes from what the user actually said — applied to a caller that
// does not have it up front.
//
// A store that never got its first Append (the user closed the TUI without
// sending anything) simply never creates a file, which is correct: there is
// nothing to resume.
type sessionRecorder struct {
	store    *convo.Store
	conv     *convo.Conversation
	model    string
	keepLast int
}

// Append implements tui.Recorder.
func (r *sessionRecorder) Append(m convo.Message) error {
	if r.conv == nil {
		conv, err := r.store.New(titleFrom(m.Text()), r.model)
		if err != nil {
			return err
		}
		r.conv = conv
	}
	if err := r.store.Append(r.conv.ID, m); err != nil {
		return err
	}
	// §5.2's keep_last: pruned after every append, same point headless.go
	// rotates from, so a long-running TUI session doesn't let the sessions
	// directory grow without bound between restarts.
	if r.keepLast > 0 {
		_, _ = r.store.Rotate(r.keepLast)
	}
	return nil
}

// NewSessionRecorder opens (or refuses to open) the session store for a TUI
// run, honouring [session] save/dir exactly the way headless.go's step 5
// does. A nil Recorder is the supported "not saving" value — either the
// config said so, or the directory could not be created — and warn carries
// the second case to the caller the same way BuildEngine's own warn string
// already does for a provider that failed to resolve.
func NewSessionRecorder(cfg *config.Config, model string) (recorder tui.Recorder, warn string) {
	if cfg == nil || !cfg.Session.Save {
		return nil, ""
	}
	dir := cfg.Session.Dir
	if dir == "" {
		dir = xdg.SessionsDir()
	}
	store, err := convo.NewStore(dir)
	if err != nil {
		return nil, fmt.Sprintf("the session will not be saved: %v", err)
	}
	return &sessionRecorder{store: store, model: model, keepLast: cfg.Session.KeepLast}, ""
}
