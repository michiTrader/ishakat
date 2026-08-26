package config

const Schema = 1

type Config struct {
	Schema    int               `toml:"schema"`
	App       App               `toml:"app"`
	Session   Session           `toml:"session"`
	UI        UI                `toml:"ui"`
	Keys      Keys              `toml:"keys"`
	Catalog   Catalog           `toml:"catalog"`
	Compact   Compact           `toml:"compact"`
	Tools     Tools             `toml:"tools"`
	Autonomy  Autonomy          `toml:"autonomy"`
	Serve     Serve             `toml:"serve"`
	Favorites Favorites         `toml:"favorites"`
	Alias     map[string]string `toml:"alias"`
	Providers []Provider        `toml:"provider"`

	// Diagnóstico de carga (no serializables)
	Files    []string          `toml:"-"`
	Warnings []Warning         `toml:"-"`
	EnvUsed  map[string]string `toml:"-"`
}

type App struct {
	DefaultModel     string `toml:"default_model"`
	CompactModel     string `toml:"compact_model"`
	FallbackModel    string `toml:"fallback_model"`
	Stream           bool   `toml:"stream"`
	SystemPrompt     string `toml:"system_prompt"`
	SystemPromptFile string `toml:"system_prompt_file"`

	// AgentsMD turns Step 18's AGENTS.md discovery off. True by default: the
	// whole feature exists so standing rules do not have to be repeated every
	// message (docs/PLAN.md §11), and a user who never created any of the
	// three files pays nothing for this being on — Resolve returns silently
	// empty. It is here, not hardcoded, for the same reason app.system_prompt
	// itself is configurable: a script driving `ishakat -p` in an
	// environment with an unrelated AGENTS.md lying around (a monorepo
	// shared with another AGENTS.md-reading tool, say) needs a way to say
	// "not for this one".
	AgentsMD bool `toml:"agents_md"`

	TimeoutS        int    `toml:"timeout_s"`
	ConnectTimeoutS int    `toml:"connect_timeout_s"`
	MaxRetries      int    `toml:"max_retries"`
	Locale          string `toml:"locale"`
}

type Session struct {
	Save       bool   `toml:"save"`
	Dir        string `toml:"dir"`
	Autoname   bool   `toml:"autoname"`
	KeepLast   int    `toml:"keep_last"`
	ResumeLast bool   `toml:"resume_last"`
}

type UI struct {
	Theme      string `toml:"theme"`
	Banner     bool   `toml:"banner"`
	Markdown   bool   `toml:"markdown"`
	Syntax     bool   `toml:"syntax"`
	Reasoning  string `toml:"reasoning"`
	Timestamps bool   `toml:"timestamps"`
	Mouse      bool   `toml:"mouse"`
	Layout     string `toml:"layout"`
	MaxWidth   int    `toml:"max_width"`
	Color      string `toml:"color"`

	// Glyphs decides which characters the interface is allowed to draw:
	// "auto" | "unicode" | "ascii". It is a separate axis from Color because a
	// terminal can paint 16 million colours and still show a box instead of
	// "▀" — which is exactly what a stock PowerShell console does, and why the
	// start-up logo was reported as unreadable.
	Glyphs string `toml:"glyphs"`

	// TUIMode is DECISION-1(d), docs/DESIGN-tui-mode.md §5: "auto" (default) |
	// "regular" | "fullscreen". "auto" is deliberately not a platform-specific
	// string — the shipped defaults.toml stays identical everywhere, and the
	// decision is made once at start-up by internal/termenv.Detect, which is
	// testable. See that package's own doc comment for what decides the
	// answer (the terminal drawing the output, not the OS running the
	// process — WSL is not a verdict either way).
	TUIMode string `toml:"tui_mode"`

	// FullscreenExitTranscript is DECISION-1b: when true (the default),
	// leaving fullscreen prints the whole conversation to the terminal's own
	// scrollback before handing it back, so nothing the session said is lost
	// with the alternate screen. Ignored in "regular", which never took the
	// scrollback away in the first place.
	FullscreenExitTranscript bool `toml:"fullscreen_exit_transcript"`

	// SteeringMode is DECISION-2 consequence 3 (docs/ROADMAP-ux-2026-08-20.md,
	// W2 item 4, F13): "one-at-a-time" | "batch". Governs how many queued
	// steering messages (ordinary text submitted with Submit while ModeBusy
	// and a tools-enabled turn is running, engine.AgentSink.Inject's own
	// hook) are handed to the running loop per Inject() poll.
	// "one-at-a-time" (the default, matching the value the original report's
	// own settings dump showed hard-coded) delivers exactly the oldest
	// queued message on each poll, leaving the rest queued for the next
	// iteration; "batch" delivers everything queued at once. Either way
	// every steering message still becomes its own convo.RoleUser history
	// entry (DECISION-2 consequence 2) — this key only decides the
	// batching, never anything about what a steering message may do.
	SteeringMode string `toml:"steering_mode"`

	// FollowupMode is SteeringMode's sibling for the *other* queue F13 asks
	// for: alt+enter-queued follow-ups, meant to run once the current turn
	// ends rather than be injected into it. "one-at-a-time" (the default)
	// submits only the oldest queued follow-up as the next turn, leaving
	// the rest queued; "batch" joins every queued follow-up into a single
	// next-turn message. A follow-up queued while no turn is running (or
	// against a plain-streaming turn, which has no Inject seam at all —
	// see SteeringMode's own comment) is simply the next thing submitted
	// once the current turn, if any, finishes.
	FollowupMode string `toml:"followup_mode"`

	Animations Animations `toml:"animations"`
	Footer     Footer     `toml:"footer"`
}

type Animations struct {
	Mode    string `toml:"mode"`
	FPS     int    `toml:"fps"`
	Spinner string `toml:"spinner"`

	// Face is reserved and has no built-in reader: no first-party animation
	// consumes it. A cursor-following-eyes animation was considered for the
	// built-in spinner and is deliberately cancelled, deferred indefinitely
	// (docs/PLAN.md §11, Phase 3) rather than shipped — the key stays in the
	// schema, defaulting to false, so a user's own theme file or a future
	// plugin surface has a place to opt into that kind of animation without
	// a schema migration.
	Face bool `toml:"face"`

	GradientScroll bool   `toml:"gradient_scroll"`
	BatterySaver   string `toml:"battery_saver"`
}

type Footer struct {
	Items []string `toml:"items"`
}

type Keys struct {
	Submit  string `toml:"submit"`
	Newline string `toml:"newline"`
	Cancel  string `toml:"cancel"`
	Quit    string `toml:"quit"`
	// QuitRepeat is how many times Quit must be pressed inside the grace
	// window to actually exit (§7.4, RC-1). The double-press semantic used
	// to be written as quit = "ctrl+c ctrl+c", which can never match
	// tea.KeyPressMsg.String() — a single chord. A number is data a
	// keypress can satisfy. 0 is treated as unset and filled by NewMap /
	// validateKeys; the shipped default is 2.
	QuitRepeat  int    `toml:"quit_repeat"`
	ClearScreen string `toml:"clear_screen"`
	ModelPicker string `toml:"model_picker"`
	ModelCycle  string `toml:"model_cycle"`
	ThemePicker string `toml:"theme_picker"`
	HistoryPrev string `toml:"history_prev"`
	HistoryNext string `toml:"history_next"`
	CopyLast    string `toml:"copy_last"`
	// ToggleFold folds/unfolds the fenced code block closest to the cursor.
	// See tui.Map.ToggleFold's own comment for why the default is ctrl+r
	// rather than ctrl+o.
	ToggleFold string `toml:"toggle_fold"`

	// QueueFollowup is W2 item 4's own chord (F13, docs/ROADMAP-ux-2026-08-20.md):
	// "alt+enter queues follow-ups" instead of submitting the input box's
	// text immediately. See tui.Map.QueueFollowup's own comment for why
	// this is a distinct chord from Submit rather than a modifier read out
	// of Submit's own keypress.
	QueueFollowup string `toml:"queue_followup"`

	// EditQueue is F13's other chord: "alt+up edits the queue" the
	// follow-up queue QueueFollowup fills. See tui.Map.EditQueue's own
	// comment.
	EditQueue string `toml:"edit_queue"`

	// ScrollUp/ScrollDown are fullscreen's own scrollback keys (Bug 1
	// fix): page the view back/forward through the transcript. Named
	// after bubbles/v2 list's own "page/scroll" precedent for pgup/pgdown
	// rather than invented fresh. Regular mode never reads these — its
	// scrollback is the terminal's own — and the mouse wheel does the
	// same thing a row at a time once fullscreen claims the mouse (see
	// tui's emit, whose MouseModeCellMotion is the other half of this
	// fix).
	ScrollUp   string `toml:"scroll_up"`
	ScrollDown string `toml:"scroll_down"`

	// EffortCycle is F9's chord (docs/ROADMAP-ux-2026-08-20.md, W5):
	// cycles the current model's effort/thinking-level through its own
	// catalog.Model.EffortLevels, the same discrete per-model vocabulary
	// the `/effort` command reads and writes. It is deliberately not
	// wired to autonomy (§5's "deliberately not in any wave" list is
	// explicit that this is a different axis from the shift+tab autonomy
	// question §21.16 decision 4 defers). See tui.Map.EffortCycle's own
	// comment for the chord choice.
	EffortCycle string `toml:"effort_cycle"`
}

type Catalog struct {
	Sources          []string `toml:"sources"`
	ModelsDevURL     string   `toml:"modelsdev_url"`
	ModelsDevMetaURL string   `toml:"modelsdev_meta_url"`
	CacheFile        string   `toml:"cache_file"`
	TTLHours         int      `toml:"ttl_h"`
	Refresh          string   `toml:"refresh"`
	OfflineOK        bool     `toml:"offline_ok"`
	HideDeprecated   bool     `toml:"hide_deprecated"`
	PreferFree       bool     `toml:"prefer_free"`

	// Curate is Layer 1 of docs/DESIGN-model-curation.md
	// (internal/catalog/curate.go's catalog.Rules), wired into
	// internal/app/catalog.go's Build() call sites. HideDeprecated above
	// stays a working alias for Curate.HideDeprecated for one release
	// (design doc §1.3), so a config.toml written before this section
	// existed keeps behaving the same way.
	Curate CatalogCurate `toml:"curate"`
}

// CatalogCurate is [catalog.curate]: which automatic rules are on, plus the
// user's own global hide/keep globs. Per-provider hide/keep lives on
// Provider itself (Provider.Hide/Provider.Keep), since §1.3's own example
// puts them inside [[provider]], not here.
type CatalogCurate struct {
	// ChatOnly drops models that cannot hold a conversation at all (design
	// doc §1.2's three-signal disjunction: non-text output modality, a
	// degenerate output limit, or an explicitly non-sampled model with no
	// tools and no structured output).
	ChatOnly bool `toml:"chat_only"`

	// HideDeprecated is Layer 1's own copy of the flag (design doc §1.3):
	// moved here so Rules carries its whole policy in one place, but the
	// top-level Catalog.HideDeprecated above is still read as a fallback
	// when this one was never set (see the wiring in internal/app/catalog.go).
	HideDeprecated bool `toml:"hide_deprecated"`

	// HideSuperseded hides "X-preview"/"X-experimental"/"X-exp" only when
	// the base id "X" also exists in the same provider (design doc §1.3:
	// this has to be relational, not name-shape, or it hides the best
	// model a provider offers).
	HideSuperseded bool `toml:"hide_superseded"`

	// HideDatedTwins hides "X-<date>" only when the undated id "X" also
	// exists in the same provider.
	HideDatedTwins bool `toml:"hide_dated_twins"`

	// HideLatest hides "X-latest"/"X:latest"/"X@latest" aliases. Off by
	// default: some users deliberately want the moving target (design doc
	// §1.3).
	HideLatest bool `toml:"hide_latest"`

	// Hide is the user's own glob list (path.Match syntax against the
	// model's WireID), merged with any per-provider Hide.
	Hide []string `toml:"hide"`

	// Keep wins over every rule above, including ChatOnly and
	// HideDeprecated.
	Keep []string `toml:"keep"`
}

type Compact struct {
	Auto          bool   `toml:"auto"`
	TriggerPct    int    `toml:"trigger_pct"`
	KeepLastTurns int    `toml:"keep_last_turns"`
	SummaryTokens int    `toml:"summary_tokens"`
	Strategy      string `toml:"strategy"`
	OnError       string `toml:"on_error"`
}

// Tools configures the agent layer of docs/PLAN.md §19: which capabilities the
// model may invoke, how much a single turn is allowed to spend doing so, and
// under what governance ishakat is allowed to write new tools for itself.
//
// The zero value is deliberately not the shipped default. Defaults come from
// defaults.toml so that a user who wants to reason about them can read them,
// which matters more here than anywhere else in the schema — these keys decide
// what a language model is permitted to do to the machine.
type Tools struct {
	// Enabled turns the whole tool layer off. With false, no tool definitions
	// are sent to the provider at all and ishakat behaves exactly like the
	// Phase 2 chat: useful for a cheap model that handles tools badly, and as
	// the blunt instrument for a user who wants none of this.
	Enabled bool `toml:"enabled"`

	// Dir is where layer-2 capabilities live (§19.1): declarative manifests and
	// script tools, one directory per tool.
	Dir string `toml:"dir"`

	// SkillsDir is where rung-0 prose capabilities live (SKILL.md files).
	SkillsDir string `toml:"skills_dir"`

	// MaxTools is the hard cap on how many tools may be active at once
	// (§19.6, gate 1). It exists because both prompt cost and the model's
	// selection accuracy degrade with catalogue size: past a few dozen
	// near-identical tools, the model starts picking the wrong one. Archived
	// tools (§19.5) do not count against it.
	MaxTools int `toml:"max_tools"`

	// ArchiveDays is how long a tool may go unused before it leaves the system
	// prompt. It is archived, never deleted: it stops costing tokens and
	// `/tools revive` brings it back.
	ArchiveDays int `toml:"archive_days"`

	// MaxCallsPerTurn is the hard cap on tool invocations in a single turn
	// (Step 14). Hitting it ends the turn with an explanation, never silently.
	MaxCallsPerTurn int `toml:"max_calls_per_turn"`

	// MaxOutputBytes truncates an oversized tool result in the middle, with a
	// marker naming how much was dropped, so that one `bash` command printing
	// 40 MB cannot destroy the context window.
	MaxOutputBytes int `toml:"max_output_bytes"`

	// BudgetUSD is the per-session spend ceiling for tool-using turns, 0 for
	// no limit. §15's runaway-cost mitigation: a stuck loop on an expensive
	// model burns real money in minutes, and the user should not learn about it
	// from the invoice.
	BudgetUSD float64 `toml:"budget_usd"`

	// TimeoutS bounds a single tool invocation.
	TimeoutS int `toml:"timeout_s"`

	// MinIntervalMS is a floor, in milliseconds, on the gap between one
	// agent-loop iteration's provider request and the next (§21.9 fix 5).
	// 0 -- the default -- disables it.
	//
	// It is off by default on purpose. §21.9's fixes 1-3 remove the
	// amplification (a denial ends the turn, a server's Retry-After is
	// honoured as a floor, a hunt of identically-failing variants stops),
	// and the test suite proves each of those with this at zero. A sleep
	// that merely hides an amplification defect makes it harder to observe
	// and returns at scale. Set this only for a provider that rate-limits
	// on requests per minute, where even a correct agent wants pacing.
	MinIntervalMS int `toml:"min_interval_ms"`

	Permissions Permissions `toml:"permissions"`
	Evolve      Evolve      `toml:"evolve"`
	Egress      Egress      `toml:"egress"`
}

// Autonomy configures §21.4 layer 3: the sticky, human-granted decision about
// how much ishakat may do without asking, for a project with no trust.json
// record yet, and whether the first-run trust dialog's own answer (§21.4
// layer 2) is worth remembering at all.
type Autonomy struct {
	// Default is the autonomy word ("auto", "agile" or "readonly", per
	// permissions.ParseAutonomy) a project falls back to before its first
	// trust decision is made -- effectively what the trust dialog's own
	// preselected option maps to when nothing has been asked yet. "ask"
	// is not an Autonomy value itself; it names the fourth state, "no
	// decision recorded, so show the dialog", which is why it is the
	// shipped default here rather than "agile".
	Default string `toml:"default"`

	// Remember controls whether the trust dialog's answer is persisted to
	// trust.json at all (§21.4 layer 2's "remembered per path" behaviour).
	// False makes every run ask again, which exists for a scripted or
	// disposable environment where a stored per-path record is pointless
	// or even undesirable (e.g. a throwaway container).
	Remember bool `toml:"remember"`
}

// Permissions is §19's danger tiering: what ishakat may do without asking.
type Permissions struct {
	// Read, Write and Shell are each "ask" | "allow" | "deny". Reading is
	// "allow" by default because a read cannot damage anything; writing and
	// shell default to "ask" because they can.
	Read  string `toml:"read"`
	Write string `toml:"write"`
	Shell string `toml:"shell"`

	// AllowSession lets an approval cover the rest of the session ("allow for
	// session"). It never applies to a danger:high tool — that carve-out is in
	// code, not configuration, precisely so it cannot be configured away
	// (§19.5, rule 2).
	AllowSession bool `toml:"allow_session"`

	// ShellDeny are command patterns refused outright, with an explanation,
	// rather than offered for confirmation. Some shapes are simply not worth a
	// prompt that a tired user will approve on autopilot.
	ShellDeny []string `toml:"shell_deny"`

	// WriteDeny are path patterns that may never be written or read as tool
	// input, regardless of any approval. §19.8's structural exfiltration
	// defence: the point of a hard block is that no amount of persuasion in the
	// context can turn it into a yes.
	WriteDeny []string `toml:"write_deny"`
}

// Evolve governs self-extension (§19.6, §19.7): whether ishakat may write new
// tools for itself, and on whose initiative.
type Evolve struct {
	// Mode is "off" | "on_request" | "suggest" | "auto".
	//
	// The default is "suggest": proactive about noticing that work has repeated
	// and offering to crystallize it, never proactive about installing. Both
	// extremes are worse. "on_request" alone means the feature is never
	// discovered, because a user busy with their actual problem does not
	// remember to ask; "auto" means losing track of what is installed, which
	// makes §19.8's prompt-injection surface unacceptable.
	Mode string `toml:"mode"`

	// MinRepeats is gate 1's repetition threshold when the *agent* takes the
	// initiative: it must prove the pattern exists before it may even ask.
	// A user declaring a recurring workflow, or forcing creation, satisfies
	// gate 1 differently — their stated intent is the evidence (§19.6).
	MinRepeats int `toml:"min_repeats"`

	// DedupThreshold is the name/description similarity above which a proposed
	// tool is treated as a duplicate, and the agent is told to extend the
	// existing tool instead of creating a sibling. This is what prevents a
	// catalogue of git_status_short / git_status_long / list_py_files.
	DedupThreshold float64 `toml:"dedup_threshold"`

	// SuggestPerSession and SuggestPerWeek bound how often a suggestion may
	// surface even when many opportunities were detected. §19.7's civility
	// rules: a proactive feature that is not rationed is Clippy.
	SuggestPerSession int `toml:"suggest_per_session"`
	SuggestPerWeek    int `toml:"suggest_per_week"`

	// DecayAfterRejects drops Mode to "on_request" after this many consecutive
	// rejections, and says so. If the user keeps declining, the agent should
	// conclude they are not interested rather than keep asking.
	DecayAfterRejects int `toml:"decay_after_rejects"`

	// RequireSelftest enforces the quarantine of §19.5: a newly written tool is
	// `unverified` and unusable until it passes its own self-test. Configurable
	// so it can be tightened, never loosened in practice — shipping false here
	// means a half-working tool can spend money on its first real call.
	RequireSelftest bool `toml:"require_selftest"`

	// AllowWithoutTTY must stay false. With no terminal there is no human to
	// authorize gate 2, so `tool_create` is denied over `ishakat -p`,
	// `ishakat serve`, cron and CI. `--yolo` does not grant it either: that
	// flag authorizes shell and writes, not self-evolution. Enabling this is
	// what `--allow-tool-create` is for — a flag a human typed knowingly into a
	// specific script (§19.7).
	AllowWithoutTTY bool `toml:"allow_without_tty"`
}

// Egress is the allowlist of hosts a declarative tool or `fetch` may reach.
// A host outside it is its own separate confirmation, never covered by a
// blanket approval (§19.8, mitigation 4).
type Egress struct {
	Allow []string `toml:"allow"`

	// AllowAll disables the allowlist. Present so that a desktop user doing
	// research is not fighting the tool, and separated from Allow so that
	// turning the safety off is a visible, single, greppable line in a config
	// file rather than an emergent consequence of a long list.
	AllowAll bool `toml:"allow_all"`
}

// Serve configures docs/PLAN.md §11 Step 23, the third door: `ishakat serve`,
// an NDJSON-over-WebSocket socket another program (a voice model, n8n, an
// editor plugin, cron) can drive the same agent loop through.
type Serve struct {
	// Addr is the listen address. Loopback by default ("127.0.0.1:20129"):
	// exposing this socket beyond the local machine is a deliberate, visible
	// edit to this one line, not an accidental consequence of running the
	// command on a machine that happens to have a routable interface.
	Addr string `toml:"addr"`

	// Token is a bearer token clients must present to open a session. Empty
	// is safe only together with a loopback Addr — validateServe warns
	// loudly if Addr is ever pointed at anything reachable from outside this
	// machine while Token is still empty.
	Token string `toml:"token"`

	// AllowToolCreate mirrors the CLI's --allow-tool-create (§19.7) for the
	// serve door specifically: it only lets tool_create appear in the
	// registry a session offers the model. Creation itself still requires an
	// explicit permission_request answered over the socket — this flag never
	// grants unattended approval, only visibility.
	AllowToolCreate bool `toml:"allow_tool_create"`

	// MaxSessions caps how many concurrent WebSocket sessions the door will
	// accept. Without a cap, a misbehaving or adversarial caller opening
	// connections in a loop is a resource-exhaustion vector it can trip
	// without meaning any harm at all.
	MaxSessions int `toml:"max_sessions"`

	// IdleTimeoutS closes a session that has sent nothing for this many
	// seconds, so a caller that vanished (crashed, network partition) does
	// not hold a slot against MaxSessions forever.
	IdleTimeoutS int `toml:"idle_timeout_s"`
}

type Favorites struct {
	List []string `toml:"list"`
}

type Provider struct {
	ID       string            `toml:"id"`
	Name     string            `toml:"name"`
	Kind     string            `toml:"kind"`
	BaseURL  string            `toml:"base_url"`
	WireAPI  string            `toml:"wire_api"`
	APIKey   string            `toml:"api_key"`
	Discover bool              `toml:"discover"`
	Enabled  bool              `toml:"enabled"`
	TimeoutS int               `toml:"timeout_s"`
	Headers  map[string]string `toml:"headers"`
	Params   map[string]any    `toml:"params"`
	Models   []ProviderModel   `toml:"model"`

	// Hide and Keep are this provider's own curation globs
	// (docs/DESIGN-model-curation.md §1.3's "per-provider policy the
	// report asked for"), merged additively with [catalog.curate]'s
	// global Hide/Keep — never override semantics, per that section's own
	// text. Read by internal/app/catalog.go into
	// catalog.Rules.Providers[p.ID].
	Hide []string `toml:"hide"`
	Keep []string `toml:"keep"`

	AuthOK     bool   `toml:"-"`
	MissingEnv string `toml:"-"`
}

type ProviderModel struct {
	ID      string   `toml:"id"`
	Name    string   `toml:"name"`
	Context int      `toml:"context"`
	Output  int      `toml:"output"`
	Tags    []string `toml:"tags"`
}

type Warning struct {
	Where string
	Msg   string
}
