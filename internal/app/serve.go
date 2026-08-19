// serve.go implements docs/PLAN.md §11 Step 23: `ishakat serve`, the third
// door (§1/§1.0) — an NDJSON-over-WebSocket socket another program (a voice
// model, n8n, an editor plugin, cron) can drive through the exact same
// engine.RunAgentTurn loop the TUI and headless doors already use. The
// engine never knows which door is driving it (§1's own framing: "three
// front doors, one brain"), and this file is careful to reuse the headless
// door's own machinery — runAgentTurnHeadless, runTurn, ResolveModelForBoot,
// SystemPrompt — rather than re-deriving a fourth copy of "config → provider
// → turn → persist".
//
// The one genuine difference from every other door, and the reason this
// file cannot simply call Headless in a loop, is §19.7's own permission
// model. Over `ishakat -p` there is no human on the other end (headless's
// own permissions.New(..., nil) call, agentturn.go's own doc comment on
// why), so tool_create always fails closed at gate 2 regardless of
// --allow-tool-create. Over `serve`, a connected client is a genuine
// decision-maker — it can answer a permission_request with a real
// permission_response — so this file wires a real, non-nil
// permissions.Reviewer (serveReviewer, below) that round-trips a
// tool-approval dialog over the WebSocket connection itself, the same
// pattern toolReviewer (toolreview.go) already establishes for the TUI's
// own tea.Program round trip.
package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichiTrader/ishakat/internal/ask"
	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/wsproto"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// ServeOptions configures `ishakat serve`. The zero value is not valid on
// its own (Config must resolve to something, either supplied or loaded from
// disk) — mirroring HeadlessOptions' own shape and reasons: the fields below
// exist so a test can drive the whole door against a real net.Listener and
// a fake provider without touching the user's real config file or a real
// port.
type ServeOptions struct {
	Version string

	// ConfigPath points at a different config.toml (--config), used only
	// when Config is nil.
	ConfigPath string
	// Config lets a caller (a test) supply an already-built configuration
	// instead of reading one from disk.
	Config *config.Config

	// Addr overrides cfg.Serve.Addr when non-empty (--addr). Left empty,
	// the configured [serve].addr is used unchanged.
	Addr string
	// Token overrides cfg.Serve.Token when non-empty (--token).
	Token string
	// AllowToolCreate overrides cfg.Serve.AllowToolCreate when set
	// (--allow-tool-create). nil means "use the config value unchanged".
	AllowToolCreate *bool

	// Listener is a test seam: when set, Serve binds to it instead of
	// calling net.Listen(cfg.Serve.Addr) itself, so a test can pick an
	// ephemeral port (":0") and read back the real one from the listener's
	// own Addr().
	Listener net.Listener

	Stdout io.Writer
	Stderr io.Writer
}

// Serve runs the WebSocket door until its context is cancelled (SIGINT,
// SIGTERM, or opts.Listener closing on its own) and returns the process
// exit code, mirroring Headless's own contract.
func Serve(opts ServeOptions) int {
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	errw := opts.Stderr
	if errw == nil {
		errw = os.Stderr
	}

	cfg := opts.Config
	if cfg == nil {
		path := opts.ConfigPath
		if path == "" {
			path = xdg.ConfigFile()
		}
		loaded, err := config.Load(config.Options{UserPath: path})
		if err != nil {
			fmt.Fprintf(errw, "✗ Configuration error: %v\n", err)
			return ExitError
		}
		cfg = loaded
	}

	addr := cfg.Serve.Addr
	if opts.Addr != "" {
		addr = opts.Addr
	}
	token := cfg.Serve.Token
	if opts.Token != "" {
		token = opts.Token
	}
	allowToolCreate := cfg.Serve.AllowToolCreate
	if opts.AllowToolCreate != nil {
		allowToolCreate = *opts.AllowToolCreate
	}

	for _, w := range cfg.Warnings {
		fmt.Fprintf(errw, "⚠ [%s] %s\n", w.Where, w.Msg)
	}

	srv := &wsServer{
		cfg:             cfg,
		version:         opts.Version,
		token:           token,
		allowToolCreate: allowToolCreate,
		maxSessions:     cfg.Serve.MaxSessions,
		idleTimeout:     time.Duration(cfg.Serve.IdleTimeoutS) * time.Second,
		out:             out,
		err:             errw,
		conns:           make(map[*wsproto.Conn]struct{}),
	}

	ln := opts.Listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(errw, "✗ could not listen on %s: %v\n", addr, err)
			return ExitError
		}
	}

	httpSrv := &http.Server{Handler: srv}

	fmt.Fprintf(out, "ishakat serve · listening on %s\n", ln.Addr())
	if token == "" {
		fmt.Fprintln(out, "  (no token configured: any client that can reach this address may open a session)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.closeAll()
		httpSrv.Close()
	}()

	err := httpSrv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(errw, "✗ serve error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// wsServer is the http.Handler behind every incoming connection: it checks
// the bearer token, enforces MaxSessions, performs the WebSocket upgrade,
// and hands the resulting *wsproto.Conn to a fresh serveSession.
type wsServer struct {
	cfg             *config.Config
	version         string
	token           string
	allowToolCreate bool
	maxSessions     int
	idleTimeout     time.Duration
	out, err        io.Writer

	activeSessions atomic.Int64

	connsMu sync.Mutex
	conns   map[*wsproto.Conn]struct{}
}

func (s *wsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && !checkBearerToken(r, s.token) {
		http.Error(w, "unauthorized: missing or incorrect bearer token", http.StatusUnauthorized)
		return
	}

	if s.maxSessions > 0 {
		for {
			cur := s.activeSessions.Load()
			if cur >= int64(s.maxSessions) {
				http.Error(w, "too many concurrent sessions", http.StatusServiceUnavailable)
				return
			}
			if s.activeSessions.CompareAndSwap(cur, cur+1) {
				break
			}
		}
		defer s.activeSessions.Add(-1)
	}

	conn, err := wsproto.Upgrade(w, r)
	if err != nil {
		http.Error(w, "expected a websocket upgrade request", http.StatusBadRequest)
		return
	}
	s.track(conn)
	defer func() {
		s.untrack(conn)
		conn.Close()
	}()

	sess := newServeSession(s.cfg, s.version, s.allowToolCreate, s.idleTimeout, conn)
	sess.run()
}

func (s *wsServer) track(c *wsproto.Conn) {
	s.connsMu.Lock()
	s.conns[c] = struct{}{}
	s.connsMu.Unlock()
}

func (s *wsServer) untrack(c *wsproto.Conn) {
	s.connsMu.Lock()
	delete(s.conns, c)
	s.connsMu.Unlock()
}

// closeAll force-closes every live connection. Called on shutdown: an
// *http.Server's own Close only tears down its listener and any connection
// still inside net/http's own request handling — a hijacked connection
// (every WebSocket here, by definition) is detached from that lifecycle the
// moment wsproto.Upgrade returns, so without this a SIGINT would leave every
// open session running until its own peer disconnects.
func (s *wsServer) closeAll() {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	for c := range s.conns {
		_ = c.Close()
	}
}

// checkBearerToken reports whether r carries want, either as
// "Authorization: Bearer <token>" (the ordinary case, for any client able to
// set custom headers on its upgrade request) or as a "?token=" query
// parameter (the fallback: a browser's own WebSocket constructor cannot set
// arbitrary headers, so a same-origin JS client has no other way to prove
// it holds the token). Comparison is constant-time so the check itself
// never becomes a timing side-channel for guessing the configured token.
func checkBearerToken(r *http.Request, want string) bool {
	got := r.URL.Query().Get("token")
	if auth := r.Header.Get("Authorization"); got == "" && strings.HasPrefix(auth, "Bearer ") {
		got = strings.TrimPrefix(auth, "Bearer ")
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ─────────────────────────────────────────────────────────────
// wire vocabulary
// ─────────────────────────────────────────────────────────────

// serveEvent is one NDJSON line this door ever sends, server → client. Its
// shape deliberately mirrors jsonEvent (sink.go)'s own --json vocabulary —
// same field names for the events both share (delta, tool_call, usage,
// done, …) — plus the events unique to a bidirectional socket: "session"
// (sent once, right after connect, in place of --json's "meta"),
// "permission_request" (§19.7's own round trip, answered by a
// permission_response client message), and "ask_request" (§21.7's own
// serve-door round trip for the model's ask_user tool, answered by an
// ask_response client message) — none of these three are ever emitted by
// --json, since headless has nothing to send them to.
type serveEvent struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	// session (sent once at connect) / meta (sent once per turn, echoing
	// the resolved model)
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	WireID   string `json:"wire_id,omitempty"`
	Session  string `json:"session,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`
	Version  string `json:"version,omitempty"`

	// tool_call / tool_result / permission_request
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
	// Error marks a failed tool_result. Reused as "denied" is not a
	// separate case: a denial is exactly a tool_result with Error=true and
	// Text carrying permissions.ErrDenied's own message, the same
	// contract runTurn/runAgentTurnHeadless already give --json.
	Error bool `json:"error,omitempty"`

	// permission_request / permission_response's own correlation id, and
	// ask_request / ask_response's own — the two round trips never share
	// an id (registerPending/registerAskPending each draw from their own
	// counter, "perm-N" vs "ask-N"), so a response naming the wrong kind's
	// id simply matches nothing, the same silently-dropped shape either
	// pending map already gives a stale or mistyped id.
	ID   string `json:"id,omitempty"`
	Tier string `json:"tier,omitempty"`

	// ask_request's own payload: form is ask.Form, marshaled with
	// encoding/json's own default field names (Title, Questions, ID,
	// Prompt, Options, AllowFreeText, Label, Value) — ask.Form carries no
	// json tags of its own (internal/ask's own presentation-free rule,
	// §6.1, stops short of dictating a wire encoding), so this is exactly
	// what json.Marshal(form) already produces without this file adding
	// any translation of its own.
	Form json.RawMessage `json:"form,omitempty"`

	Usage *convo.Usage `json:"usage,omitempty"`

	Aborted bool  `json:"aborted,omitempty"`
	MS      int64 `json:"ms,omitempty"`
}

// clientMsg is one NDJSON line this door ever accepts, client → server.
// Type selects which of the fields below apply; unrecognised fields for the
// other type are simply ignored, matching encoding/json's own default
// behaviour and sparing this door a second struct.
type clientMsg struct {
	Type string `json:"type"`

	// "prompt"
	Text   string `json:"text,omitempty"`
	Model  string `json:"model,omitempty"`
	System string `json:"system,omitempty"`

	// "permission_response"
	ID           string `json:"id,omitempty"`
	Allow        bool   `json:"allow,omitempty"`
	AllowSession bool   `json:"allow_session,omitempty"`

	// "ask_response": Answers is ask.Answers (map[string]ask.Answer),
	// keyed by each Question.ID the matching ask_request's own Form
	// carried — the client answers by echoing those same ids back, the
	// identical shape ask.State.Submit itself produces on the TUI side of
	// this same primitive. ID (above) is this response's own correlation
	// id, shared with the same field permission_response already uses,
	// but drawn from registerAskPending's own separate counter (see
	// serveEvent.ID's own doc comment).
	Answers json.RawMessage `json:"answers,omitempty"`
}

// tierName keeps emitting the wire protocol's existing "low"/"medium"/
// "high" strings rather than renaming them to "safe"/"sensitive"/
// "critical" -- this is a server-side risk-class rename, not a wire
// protocol version bump. Safe->"low", Sensitive->"medium", Critical->
// "high"; Controlled (and any future value) falls into the default
// "medium", matching Controlled's own review-skipping behavior being
// closer to Safe in practice but not worth a fifth wire value yet.
func tierName(t permissions.Tier) string {
	switch t {
	case permissions.Safe:
		return "low"
	case permissions.Critical:
		return "high"
	default:
		return "medium"
	}
}

// ─────────────────────────────────────────────────────────────
// per-connection session
// ─────────────────────────────────────────────────────────────

// serveSession is one open WebSocket connection's whole lifetime: the
// resolved model/provider (cached across turns on this connection, unlike
// Headless's one-resolution-per-process default), the persistent
// conversation both sides keep building, the permissions.Guard — with a
// real serveReviewer attached, per this file's own doc comment — and the
// bookkeeping a permission_response client message needs to find the
// pending Review call it answers.
type serveSession struct {
	cfg             *config.Config
	version         string
	allowToolCreate bool
	idleTimeout     time.Duration
	conn            *wsproto.Conn

	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan permissions.Decision
	nextID    atomic.Uint64

	// pendingAsk/nextAskID are ask_request/ask_response's own separate
	// bookkeeping, parallel to pending/nextID above rather than reusing
	// them: permissions.Decision and ask.Answers are unrelated shapes (a
	// yes/no versus a map of arbitrary answers), so one channel type
	// cannot serve both round trips, and giving each its own id counter
	// (serveAsker.newRequestID's own "ask-N" versus serveReviewer's own
	// "perm-N") means a stray response naming an id from the wrong round
	// trip matches nothing in either map, rather than needing a type
	// assertion here to tell them apart.
	pendingAskMu sync.Mutex
	pendingAsk   map[string]chan ask.Answers
	nextAskID    atomic.Uint64

	turnActive atomic.Bool

	guard *permissions.Guard
	asker *serveAsker

	store *convo.Store
	conv  *convo.Conversation
	hist  *convo.Conversation

	resolved  bool
	ref       ModelRef
	prov      provider.Provider
	modelCost *catalog.Cost
	modelCaps tools.Caps
	system    string
}

func newServeSession(cfg *config.Config, version string, allowToolCreate bool, idleTimeout time.Duration, conn *wsproto.Conn) *serveSession {
	ctx, cancel := context.WithCancel(context.Background())
	sess := &serveSession{
		cfg:             cfg,
		version:         version,
		allowToolCreate: allowToolCreate,
		idleTimeout:     idleTimeout,
		conn:            conn,
		ctx:             ctx,
		cancel:          cancel,
		pending:         make(map[string]chan permissions.Decision),
		pendingAsk:      make(map[string]chan ask.Answers),
	}
	sess.guard = permissions.New(cfg.Tools.Permissions, false, &serveReviewer{sess: sess})
	// asker is always built, mirroring serveReviewer immediately above:
	// §21.7's own door table gives serve "yes, over WS" for "ask
	// available?" unconditionally, the same way a real, non-nil
	// permissions.Reviewer is always wired here regardless of whether any
	// given connection ever actually triggers either round trip.
	sess.asker = &serveAsker{sess: sess}
	return sess
}

// run is the connection's read loop: one goroutine, reading messages and
// either starting a turn (in its own goroutine, so a slow tool call never
// blocks this loop from noticing a permission_response) or resolving a
// pending Review call. It returns once the connection closes, by any
// cause — the peer's own Close frame, the idle timer, or the server's own
// shutdown closing every tracked *wsproto.Conn.
func (sess *serveSession) run() {
	defer sess.cancel()

	sess.sendEvent(serveEvent{Type: "hello", Version: sess.version})

	var idleTimer *time.Timer
	if sess.idleTimeout > 0 {
		idleTimer = time.AfterFunc(sess.idleTimeout, func() { sess.conn.Close() })
		defer idleTimer.Stop()
	}

	for {
		op, payload, err := sess.conn.ReadMessage()
		if err != nil {
			return
		}
		if idleTimer != nil {
			idleTimer.Reset(sess.idleTimeout)
		}
		if op != wsproto.OpText {
			continue
		}

		var msg clientMsg
		if err := json.Unmarshal(payload, &msg); err != nil {
			sess.sendEvent(serveEvent{Type: "error", Text: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}

		switch msg.Type {
		case "prompt":
			sess.handlePrompt(msg)
		case "permission_response":
			sess.resolvePending(msg.ID, permissions.Decision{Allow: msg.Allow, AllowSession: msg.AllowSession})
		case "ask_response":
			var answers ask.Answers
			if len(msg.Answers) > 0 {
				if err := json.Unmarshal(msg.Answers, &answers); err != nil {
					sess.sendEvent(serveEvent{Type: "error", Text: fmt.Sprintf("ask_response: invalid answers: %v", err)})
					continue
				}
			}
			sess.resolveAskPending(msg.ID, answers)
		default:
			sess.sendEvent(serveEvent{Type: "error", Text: fmt.Sprintf("unknown message type %q", msg.Type)})
		}
	}
}

// handlePrompt starts one turn, refusing a second one that arrives while
// the first is still running: RunAgentTurn owns *hist for the whole turn,
// and two goroutines appending to it concurrently would race. A client that
// wants concurrent turns opens a second connection — sessions, not turns,
// are this door's unit of concurrency (MaxSessions, not a per-connection
// turn queue).
func (sess *serveSession) handlePrompt(msg clientMsg) {
	if strings.TrimSpace(msg.Text) == "" {
		sess.sendEvent(serveEvent{Type: "error", Text: "prompt: text must not be empty"})
		return
	}
	if !sess.turnActive.CompareAndSwap(false, true) {
		sess.sendEvent(serveEvent{Type: "error", Text: "a turn is already in progress on this connection"})
		return
	}
	go func() {
		defer sess.turnActive.Store(false)
		sess.runTurn(msg)
	}()
}

// runTurn is this door's own version of Headless's steps 4-8, adapted for a
// connection that outlives any single turn: resolve the model (once, then
// cached — a per-turn Model override re-resolves), open the session file
// (once), run the turn through the exact same runAgentTurnHeadless /
// runTurn pair Headless itself calls, and persist.
func (sess *serveSession) runTurn(msg clientMsg) {
	if err := sess.resolveModel(msg.Model); err != nil {
		sess.sendEvent(serveEvent{Type: "error", Text: err.Error()})
		return
	}

	system := strings.TrimSpace(msg.System)
	if system == "" {
		system = sess.system
	}

	if err := sess.ensureSession(msg.Text); err != nil {
		sess.sendEvent(serveEvent{Type: "warning", Text: fmt.Sprintf("the session will not be saved: %v", err)})
	}

	s := &wsSink{sess: sess}
	stream := sess.cfg.App.Stream
	sessionID := ""
	if sess.conv != nil {
		sessionID = sess.conv.ID
	}
	s.meta(sess.ref, sessionID, stream)

	user := convo.User(msg.Text)
	if sess.store != nil && sess.conv != nil {
		if err := sess.store.Append(sess.conv.ID, user); err != nil {
			s.warn(fmt.Sprintf("could not save the user's message: %v", err))
		}
	}

	req := provider.Request{
		Model:    sess.ref.WireID,
		Messages: []convo.Message{user},
		System:   system,
		Stream:   stream,
		// Same [ui].reasoning answer every other door gives, so a session over
		// the WebSocket bridge is not the one place reasoning goes missing.
		IncludeReasoning: ReasoningWanted(sess.cfg),
	}

	started := time.Now()
	var out convo.Message
	var turnErr error
	if sess.cfg.Tools.Enabled {
		hist := sess.hist
		if hist == nil {
			hist = &convo.Conversation{}
		}
		out, turnErr = runAgentTurnHeadless(sess.ctx, sess.prov, sess.cfg.Tools, sess.guard, sess.modelCost, sess.modelCaps,
			sess.cfg.App.MaxRetries, req, user, s, sess.store, sess.conv, hist, sess.allowToolCreate, sess.asker)
	} else {
		out, turnErr = runTurn(sess.ctx, sess.prov, req, s, sess.cfg.App.MaxRetries)
	}
	out.Model = sess.ref.Ref
	elapsed := time.Since(started)

	if sess.ctx.Err() != nil {
		out.Aborted = true
	}

	if !sess.cfg.Tools.Enabled && sess.store != nil && sess.conv != nil && (len(out.Blocks) > 0 || out.Aborted) {
		if err := sess.store.Append(sess.conv.ID, out); err != nil {
			s.warn(fmt.Sprintf("could not save the response: %v", err))
		}
	}
	if sess.store != nil && sess.conv != nil {
		if n := sess.cfg.Session.KeepLast; n > 0 {
			_, _ = sess.store.Rotate(n)
		}
	}

	if turnErr != nil {
		s.fail(turnErr)
	}
	s.done(out, elapsed)
}

// resolveModel resolves modelText (empty means "use app.default_model",
// same rule as ResolveModelForBoot) and caches the result on sess so a
// second turn on the same connection with no Model override skips
// re-resolving. An explicit, different modelText always re-resolves —
// mid-connection model switching is a deliberate capability of a
// persistent door, unlike headless's one-resolution-per-process contract.
func (sess *serveSession) resolveModel(modelText string) error {
	if sess.resolved && modelText == "" {
		return nil
	}

	catalogSnapshot := LoadCatalog(sess.cfg)
	ref, fb, err := ResolveModelForBoot(sess.cfg, &catalogSnapshot.Catalog, modelText)
	if err != nil {
		return err
	}
	if line := fb.Describe(); line != "" {
		sess.sendEvent(serveEvent{Type: "warning", Text: line})
	}

	var cost *catalog.Cost
	var caps tools.Caps
	if model, found := catalogSnapshot.Catalog.Get(ref.Ref); found {
		cost = model.Cost
		caps = capsForTools(model)
	}

	pc, ok := FindProvider(sess.cfg, ref.Provider)
	if !ok {
		return fmt.Errorf("provider %q for %q is not declared in %s", ref.Provider, ref.Ref, configOrigin(sess.cfg))
	}
	prov, err := NewProvider(sess.cfg, pc, sess.version)
	if err != nil {
		return err
	}

	if !sess.resolved {
		system, warn := SystemPrompt(sess.cfg)
		if warn != "" {
			sess.sendEvent(serveEvent{Type: "warning", Text: warn})
		}
		sess.system = system
	}

	sess.ref = ref
	sess.prov = prov
	sess.modelCost = cost
	sess.modelCaps = caps
	sess.resolved = true
	return nil
}

// ensureSession opens the session file on the first turn only (title comes
// from that first turn's prompt, exactly like Headless's own openSession
// call), and is a no-op on every later turn on the same connection —
// unlike headless mode, a `serve` connection is one long-running
// conversation, not a fresh process per turn, so there is exactly one
// session file for the whole connection.
func (sess *serveSession) ensureSession(prompt string) error {
	if sess.store != nil || !sess.cfg.Session.Save {
		return nil
	}
	dir := sess.cfg.Session.Dir
	if dir == "" {
		dir = xdg.SessionsDir()
	}
	store, conv, err := openSession(dir, prompt, sess.ref.Ref)
	if err != nil {
		return err
	}
	sess.store = store
	sess.conv = conv
	sess.hist = conv
	return nil
}

// sendEvent serialises ev and writes it as one WebSocket text frame,
// serialising access to conn.WriteMessage: multiple goroutines can reach
// this concurrently (the read loop's own error/warning replies, and
// whichever goroutine is mid-turn), and *wsproto.Conn's own doc comment is
// explicit that concurrent writers among themselves are not safe.
func (sess *serveSession) sendEvent(ev serveEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	sess.writeMu.Lock()
	_ = sess.conn.WriteMessage(wsproto.OpText, b)
	sess.writeMu.Unlock()
}

// registerPending/resolvePending are the two halves of the
// permission_request/permission_response round trip: serveReviewer.Review
// registers a channel keyed by a fresh id before sending the request event,
// and the read loop's own permission_response branch (run, above) looks
// that id up and sends the client's decision on it. A response naming an id
// that was never registered (a stale retry, a typo) is silently dropped —
// there is nothing waiting on it, and erroring here would just be more
// noise for a client that already can't do anything about it.
func (sess *serveSession) registerPending(id string) chan permissions.Decision {
	ch := make(chan permissions.Decision, 1)
	sess.pendingMu.Lock()
	sess.pending[id] = ch
	sess.pendingMu.Unlock()
	return ch
}

func (sess *serveSession) unregisterPending(id string) {
	sess.pendingMu.Lock()
	delete(sess.pending, id)
	sess.pendingMu.Unlock()
}

func (sess *serveSession) resolvePending(id string, d permissions.Decision) {
	sess.pendingMu.Lock()
	ch, ok := sess.pending[id]
	sess.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- d:
	default:
	}
}

func (sess *serveSession) newRequestID() string {
	return fmt.Sprintf("perm-%d", sess.nextID.Add(1))
}

// registerAskPending/unregisterAskPending/resolveAskPending/
// newAskRequestID are ask_request/ask_response's own round trip,
// parallel to registerPending/unregisterPending/resolvePending/
// newRequestID above but keyed into pendingAsk (chan ask.Answers) instead
// of pending (chan permissions.Decision) — see serveSession's own
// pendingAsk field comment for why these stay two separate maps rather
// than one generic one.
func (sess *serveSession) registerAskPending(id string) chan ask.Answers {
	ch := make(chan ask.Answers, 1)
	sess.pendingAskMu.Lock()
	sess.pendingAsk[id] = ch
	sess.pendingAskMu.Unlock()
	return ch
}

func (sess *serveSession) unregisterAskPending(id string) {
	sess.pendingAskMu.Lock()
	delete(sess.pendingAsk, id)
	sess.pendingAskMu.Unlock()
}

func (sess *serveSession) resolveAskPending(id string, answers ask.Answers) {
	sess.pendingAskMu.Lock()
	ch, ok := sess.pendingAsk[id]
	sess.pendingAskMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- answers:
	default:
	}
}

func (sess *serveSession) newAskRequestID() string {
	return fmt.Sprintf("ask-%d", sess.nextAskID.Add(1))
}

// ─────────────────────────────────────────────────────────────
// permissions.Reviewer over the socket
// ─────────────────────────────────────────────────────────────

// serveReviewer implements permissions.Reviewer by sending a
// permission_request event over the session's own connection and blocking
// on either the matching permission_response or ctx.Done(), whichever
// comes first — the same shape toolReviewer (toolreview.go) gives the TUI,
// with tea.Program.Send/reply replaced by sendEvent/registerPending. This
// is what makes §19.7's "no human, no self-extension" rule resolve
// correctly over `serve` without weakening it: a serve connection with a
// human (or an equivalent decision-maker) actually driving the client can
// answer Yes, exactly as it could in the TUI; a connection with nothing
// reading its own permission_request events simply times out or hangs at
// ctx.Done(), the same fail-closed outcome as Headless's own nil-reviewer
// branch — never a decision that resolves itself.
type serveReviewer struct {
	sess *serveSession
}

func (r *serveReviewer) Review(ctx context.Context, req permissions.Request) (permissions.Decision, error) {
	id := r.sess.newRequestID()
	reply := r.sess.registerPending(id)
	defer r.sess.unregisterPending(id)

	r.sess.sendEvent(serveEvent{
		Type: "permission_request", ID: id, Name: req.Name, Args: req.Arguments, Tier: tierName(req.Tier),
	})

	select {
	case d := <-reply:
		return d, nil
	case <-ctx.Done():
		return permissions.Decision{}, ctx.Err()
	}
}

// ─────────────────────────────────────────────────────────────
// ask.Asker over the socket
// ─────────────────────────────────────────────────────────────

// serveAsker implements ask.Asker by sending an ask_request event over the
// session's own connection and blocking on either the matching
// ask_response or ctx.Done(), whichever comes first — serveReviewer's own
// direct sibling (its own doc comment explains the shared shape in full),
// this time carrying a serialized ask.Form out and ask.Answers back
// instead of a permissions.Request/Decision pair. Built with
// ask.AwaitReply rather than hand-rolled, the same choice tuiAsker.Ask
// (askuser.go) already made — see AwaitReply's own doc comment for why
// this is the second, not the first, caller it was written for.
//
// This is what closes §21.7's own door table for `serve`: "ask available?
// yes, over WS" — a connected client answering ask_request is exactly as
// genuine a decision-maker for the model's own ask_user tool as it
// already is for a permission_request, the same reasoning serveReviewer's
// own doc comment gives, applied to the ask primitive's other producer.
type serveAsker struct {
	sess *serveSession
}

func (a *serveAsker) Ask(ctx context.Context, form ask.Form) (ask.Answers, error) {
	formJSON, err := json.Marshal(form)
	if err != nil {
		return nil, fmt.Errorf("serve: could not encode ask.Form: %w", err)
	}

	id := a.sess.newAskRequestID()
	reply := a.sess.registerAskPending(id)
	defer a.sess.unregisterAskPending(id)

	return ask.AwaitReply(ctx, reply, func() {
		a.sess.sendEvent(serveEvent{Type: "ask_request", ID: id, Form: formJSON})
	})
}

// ─────────────────────────────────────────────────────────────
// sink over the socket
// ─────────────────────────────────────────────────────────────

// wsSink implements the sink interface (sink.go) by emitting serveEvent
// lines instead of writing to an io.Writer. Reusing the sink interface
// (rather than giving runAgentTurnHeadless/runTurn a third signature) is
// exactly why this file did not have to duplicate either function: both
// already only ever talk to their caller through this interface.
type wsSink struct {
	sess *serveSession

	// warnedSeen mirrors jsonSink's own field (sink.go): the same run can
	// call warn with an identical string more than once, and a client has
	// just as little use for the same line twice as a --json consumer does.
	warnedSeen map[string]bool
}

func (w *wsSink) meta(ref ModelRef, sessionID string, stream bool) {
	s := stream
	w.sess.sendEvent(serveEvent{
		Type: "meta", Model: ref.Ref, Provider: ref.Provider, WireID: ref.WireID,
		Session: sessionID, Stream: &s,
	})
}

func (w *wsSink) delta(s string) {
	if s != "" {
		w.sess.sendEvent(serveEvent{Type: "delta", Text: s})
	}
}

func (w *wsSink) reasoning(s string) {
	if s != "" {
		w.sess.sendEvent(serveEvent{Type: "reasoning", Text: s})
	}
}

func (w *wsSink) tool(name string, args json.RawMessage) {
	w.sess.sendEvent(serveEvent{Type: "tool_call", Name: name, Args: args})
}

func (w *wsSink) toolResult(name string, isError bool, output string) {
	w.sess.sendEvent(serveEvent{Type: "tool_result", Name: name, Text: output, Error: isError})
}

func (w *wsSink) usage(u *convo.Usage) {
	if u != nil {
		w.sess.sendEvent(serveEvent{Type: "usage", Usage: u})
	}
}

func (w *wsSink) warn(s string) {
	if s == "" {
		return
	}
	if w.warnedSeen == nil {
		w.warnedSeen = map[string]bool{}
	}
	if w.warnedSeen[s] {
		return
	}
	w.warnedSeen[s] = true
	w.sess.sendEvent(serveEvent{Type: "warning", Text: s})
}

func (w *wsSink) fail(err error) {
	if err != nil {
		w.sess.sendEvent(serveEvent{Type: "error", Text: err.Error()})
	}
}

func (w *wsSink) done(msg convo.Message, elapsed time.Duration) {
	w.sess.sendEvent(serveEvent{
		Type:    "done",
		Text:    msg.Text(),
		Usage:   msg.Usage,
		Aborted: msg.Aborted,
		MS:      elapsed.Milliseconds(),
	})
}
