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
	Submit      string `toml:"submit"`
	Newline     string `toml:"newline"`
	Cancel      string `toml:"cancel"`
	Quit        string `toml:"quit"`
	ClearScreen string `toml:"clear_screen"`
	ModelPicker string `toml:"model_picker"`
	ModelCycle  string `toml:"model_cycle"`
	ThemePicker string `toml:"theme_picker"`
	HistoryPrev string `toml:"history_prev"`
	HistoryNext string `toml:"history_next"`
	CopyLast    string `toml:"copy_last"`
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
