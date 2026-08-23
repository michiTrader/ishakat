// session.go wires the TUI's persistence (§10, Step 13) over convo.Store.
// This is the only place allowed to know both what a session file is and
// what tui.Recorder expects — app.go's usual role of cabling config into
// something the TUI can hold without importing convo/xdg itself.
package app

import (
	"errors"
	"fmt"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// sessionRecorder adapts *convo.Store to tui.Recorder.
//
// conv starts nil for a fresh session: the file is created lazily, on the
// first Append, rather than eagerly at Run. At NewRoot time nothing has been
// typed yet, so there is no title to give convo.Store.New the way
// headless.go's openSession has one immediately (the whole prompt is
// already on the command line there). Waiting for the first message is not
// a workaround, it is the same rule headless follows — title comes from
// what the user actually said — applied to a caller that does not have it
// up front.
//
// A store that never got its first Append (the user closed the TUI without
// sending anything) simply never creates a file, which is correct: there is
// nothing to resume.
//
// conv starts non-nil instead when ResumeSession found a previous
// conversation to reopen (--resume, resume_last): Append below only creates
// a new conversation when conv is nil, so a resumed recorder appends to the
// existing file from its very first call, exactly like store.New would have
// for a brand-new one — the two paths share every line after that check.
type sessionRecorder struct {
	store    *convo.Store
	conv     *convo.Conversation
	model    string
	keepLast int

	// pendingTitle is a title set via /name (SetTitle, below) before the
	// session file exists yet — the TUI's own NewRoot-time gap conv's own
	// doc comment describes, where there is nothing on disk to rewrite
	// because nothing has been typed yet either. Lazy creation (the two
	// "if r.conv == nil" branches below) consults this instead of
	// titleFrom once it is set, so an explicit /name before the first
	// message wins over the inferred first-line title exactly the way a
	// user's own /model pick already outranks autoselection.
	pendingTitle string
}

// firstConvTitle is the title lazy creation gives a brand-new session:
// pendingTitle (an explicit /name typed before anything else was sent)
// if set, otherwise the ordinary titleFrom guess.
func (r *sessionRecorder) firstConvTitle(fallback string) string {
	if r.pendingTitle != "" {
		return r.pendingTitle
	}
	return titleFrom(fallback)
}

// Append implements tui.Recorder.
func (r *sessionRecorder) Append(m convo.Message) error {
	if r.conv == nil {
		conv, err := r.store.New(r.firstConvTitle(m.Text()), r.model)
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

// AppendMission implements tui.MissionRecorder (§21.16 decision 3) — the
// exact mirror of Append above, for the other event kind. It shares
// Append's own lazy-creation rule rather than requiring the user's own
// message to have been recorded first: resolveToolScope (internal/tui's
// toolscope.go) calls recordMission before submit ever calls recordMessage
// for the same turn, since the two dialogs (and their resolution) happen
// while submit itself is still paused, before the user's own message is
// even added to m.conv. A fresh session's very first turn can therefore
// reach here with r.conv still nil — titleFrom(ev.Goal) gives it the exact
// same title the session would have gotten from Append a few lines later
// this same turn (ev.Goal is the goal text resolveToolScope paused, which
// is the same text submit's own Append call will see next), so the two
// lazy-creation paths agree on a title rather than one winning arbitrarily
// depending on which of the two happens to run first in a given turn.
func (r *sessionRecorder) AppendMission(ev convo.MissionEvent) error {
	if r.conv == nil {
		conv, err := r.store.New(r.firstConvTitle(ev.Goal), r.model)
		if err != nil {
			return err
		}
		r.conv = conv
	}
	if err := r.store.AppendMission(r.conv.ID, ev); err != nil {
		return err
	}
	if r.keepLast > 0 {
		_, _ = r.store.Rotate(r.keepLast)
	}
	return nil
}

// SetTitle implements tui.SessionTitleStore (F12's /name, session.go's own
// doc comment on the seam): the one write convo.Store.SetTitle has always
// offered but that, before this, nothing in the running program ever
// called — grep confirms it (§17's F12 investigation): only store.go's own
// definition and doc comments named it.
//
// Two cases, mirroring Append's own "conv starts nil" split (this struct's
// own doc comment): a session already on disk (conv != nil, the ordinary
// case once at least one message has been recorded) renames it immediately
// via the store's own rewrite. A session that has not been created yet —
// /name typed before the very first message, in the brief window between
// NewRoot and submit — has nothing to rewrite, so the title is remembered
// in pendingTitle instead and applied by Append/AppendMission's own lazy
// creation the moment there is finally a file to create it with; it does
// not fabricate a conversation just to hold a name nobody has started yet.
func (r *sessionRecorder) SetTitle(title string) error {
	if r.conv == nil {
		r.pendingTitle = title
		return nil
	}
	if err := r.store.SetTitle(r.conv.ID, title); err != nil {
		return err
	}
	r.conv.Title = title
	return nil
}

// NewSessionRecorder opens (or refuses to open) the session store for a TUI
// run, honouring [session] save/dir exactly the way headless.go's step 5
// does. A nil Recorder is the supported "not saving" value — either the
// config said so, or the directory could not be created — and warn carries
// the second case to the caller the same way BuildEngine's own warn string
// already does for a provider that failed to resolve.
//
// resumed is the conversation ResumeSession already loaded, or nil for a
// fresh session. Passing it here — rather than having the caller Append to
// it directly — keeps sessionRecorder the only place that decides between
// "create on first Append" and "append to what's already there", the same
// way it was already the only place that knew about titleFrom.
func NewSessionRecorder(cfg *config.Config, model string, resumed *convo.Conversation) (recorder tui.Recorder, warn string) {
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
	return &sessionRecorder{store: store, conv: resumed, model: model, keepLast: cfg.Session.KeepLast}, ""
}

// sessionLister adapts *convo.Store to tui.SessionLister — the read-side
// mirror of sessionRecorder above. It carries nothing but the store: List
// and Load both delegate straight through, translating convo.Header rows
// into tui.SessionSummary so internal/tui never has to know what a
// convo.Header is (§6.1's boundary, the same rule Recorder already draws
// for the write side).
type sessionLister struct {
	store *convo.Store
}

// List implements tui.SessionLister. convo.Store.List already returns
// headers most-recently-updated first (its own comment), so there is
// nothing left to sort here — only to reshape each Header into the smaller
// struct the TUI's menu actually draws.
func (l *sessionLister) List() ([]tui.SessionSummary, error) {
	headers, err := l.store.List()
	if err != nil {
		return nil, err
	}
	rows := make([]tui.SessionSummary, 0, len(headers))
	for _, h := range headers {
		rows = append(rows, tui.SessionSummary{ID: h.ID, Title: h.Title, UpdatedAt: h.UpdatedAt})
	}
	return rows, nil
}

// Load implements tui.SessionLister. Store.Load already does the real
// work (full read, truncation tolerance); this is a bare delegation.
func (l *sessionLister) Load(id string) (*convo.Conversation, error) {
	return l.store.Load(id)
}

// NewSessionLister opens (or refuses to open) the session store for
// /resume, honouring [session] save exactly like NewSessionRecorder does
// for the write side. A nil SessionLister is the supported "cannot list or
// reopen sessions" value — same rule NewSessionRecorder's own comment
// documents — and warn carries the reason to the caller the same way.
//
// existing is a *convo.Store the caller already opened on the same
// directory — ResumeSession's own return value, when --resume or
// resume_last already ran — reused instead of opening a second one:
// convo.Store carries no per-conversation state (§10), so this is purely
// to avoid a redundant os.MkdirAll, the same reasoning app.go's own comment
// gives for reusing resumeStore when it builds the recorder.
func NewSessionLister(cfg *config.Config, existing *convo.Store) (lister tui.SessionLister, warn string) {
	if existing != nil {
		return &sessionLister{store: existing}, ""
	}
	if cfg == nil || !cfg.Session.Save {
		return nil, ""
	}
	dir := cfg.Session.Dir
	if dir == "" {
		dir = xdg.SessionsDir()
	}
	store, err := convo.NewStore(dir)
	if err != nil {
		return nil, fmt.Sprintf("/resume will not be available: %v", err)
	}
	return &sessionLister{store: store}, ""
}

// ResumeSession loads the conversation --resume or [session] resume_last
// asks for. It returns (nil, nil, "") when neither applies — the ordinary
// fresh-session case — so callers can treat the three-value return as
// "conversation, or nothing, or a reason there is nothing" without a
// separate bool.
//
// A store that cannot be opened, or a directory with no sessions in it yet,
// both resolve to (nil, nil, warn-or-empty) rather than an error: the same
// rule NewSessionRecorder already applies to a fresh session — a TUI that
// cannot resume is still strictly better than one that refuses to start —
// applied to the load side instead of the save side. ErrNotFound
// specifically (no sessions on disk yet) is not even a warning: it is the
// ordinary state of a brand-new install with resume_last on, not a failure.
func ResumeSession(cfg *config.Config, resumeFlag bool) (conv *convo.Conversation, store *convo.Store, warn string) {
	if cfg == nil || !cfg.Session.Save || !(resumeFlag || cfg.Session.ResumeLast) {
		return nil, nil, ""
	}
	dir := cfg.Session.Dir
	if dir == "" {
		dir = xdg.SessionsDir()
	}
	st, err := convo.NewStore(dir)
	if err != nil {
		return nil, nil, fmt.Sprintf("could not resume the last session: %v", err)
	}
	latest, err := st.Latest()
	if err != nil {
		if errors.Is(err, convo.ErrNotFound) {
			return nil, nil, ""
		}
		return nil, nil, fmt.Sprintf("could not resume the last session: %v", err)
	}
	return latest, st, ""
}
