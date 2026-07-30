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
	Theme      string     `toml:"theme"`
	Banner     bool       `toml:"banner"`
	Markdown   bool       `toml:"markdown"`
	Syntax     bool       `toml:"syntax"`
	Reasoning  string     `toml:"reasoning"`
	Timestamps bool       `toml:"timestamps"`
	Mouse      bool       `toml:"mouse"`
	Layout     string     `toml:"layout"`
	MaxWidth   int        `toml:"max_width"`
	Color      string     `toml:"color"`
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
	Sources           []string `toml:"sources"`
	ModelsDevURL      string   `toml:"modelsdev_url"`
	ModelsDevMetaURL  string   `toml:"modelsdev_meta_url"`
	CacheFile         string   `toml:"cache_file"`
	TTLHours          int      `toml:"ttl_h"`
	Refresh           string   `toml:"refresh"`
	OfflineOK         bool     `toml:"offline_ok"`
	HideDeprecated    bool     `toml:"hide_deprecated"`
	PreferFree        bool     `toml:"prefer_free"`
}

type Compact struct {
	Auto          bool   `toml:"auto"`
	TriggerPct    int    `toml:"trigger_pct"`
	KeepLastTurns int    `toml:"keep_last_turns"`
	SummaryTokens int    `toml:"summary_tokens"`
	Strategy      string `toml:"strategy"`
	OnError       string `toml:"on_error"`
}

type Favorites struct {
	List []string `toml:"list"`
}

type Provider struct {
	ID       string            `toml:"id"`
	Name     string            `toml:"name"`
	Kind     string            `toml:"kind"`
	BaseURL  string            `toml:"base_url"`
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
