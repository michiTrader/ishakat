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
	TimeoutS         int    `toml:"timeout_s"`
	ConnectTimeoutS  int    `toml:"connect_timeout_s"`
	MaxRetries       int    `toml:"max_retries"`
	Locale           string `toml:"locale"`
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
	Mode           string `toml:"mode"`
	FPS            int    `toml:"fps"`
	Spinner        string `toml:"spinner"`
	Face           bool   `toml:"face"`
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

	Permissions Permissions `toml:"permissions"`
	Evolve      Evolve      `toml:"evolve"`
	Egress      Egress      `toml:"egress"`
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
