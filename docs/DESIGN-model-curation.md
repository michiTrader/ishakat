# Design · Catalog curation: how a provider's model list stops being noise

**Status: design only. Nothing here is implemented.** This document exists to be
argued with before a line of code is written. Every number in §1 was measured
against the live `https://models.dev/api.json` payload, not estimated, and the
measurements are what decide the shape of the proposal — in two places they
killed the design I started with.

It answers one report — "adding the Gemini API loads a pile of models and many of
them are discontinued; can they be removed, or can the user remove them from the
`/model` panel, because this generates a lot of noise?" — and then the second half
of the same question: what else should be built around it, and in what order.

Companion reading: `docs/PLAN.md` §4bis (the catalog contract), §4.5 (resolution),
§9.4 (the picker), §5.2 (configuration), §13 (command surface). This document does
not change any of those contracts; where it extends one, it says so explicitly.

---

## 1. Diagnosis, measured

Measured against `https://models.dev/api.json` on 2026-08-05: 3.5 MB, **180
providers, 6.106 model records**.

### 1.1 The first finding is a bug, not a missing feature

`[catalog] hide_deprecated = true` is **already the default**
(`internal/config/defaults.toml:67`), and `Build` honours it
(`internal/catalog/merge.go:157`). So why does the user still see discontinued
models?

Because nothing ever tags them. `TagDeprecated` is applied in exactly one place —
`applyRaw`, reading a `"deprecated": true` field out of the provider's own
`GET /models` response (`merge.go:253`). Google's OpenAI-compatible shim does not
send that field. Neither does OpenAI's, or NVIDIA's. In practice the flag fires
only for gateways that volunteer it, which is almost none of them.

Meanwhile models.dev **does** carry the information, in a field ishakat parses
right past:

| models.dev `status` | records |
|---|---|
| `deprecated` | 172 |
| `beta` | 53 |
| `alpha` | 1 |
| (absent) | 5.880 |
| **total** | **6.106** |

`wireMDModelRaw` (`internal/catalog/modelsdev.go:141`) has no `Status` field —
`grep -c status internal/catalog/modelsdev.go` returns **0** — so `MDModel` has
none, so `applyModelsDev` cannot tag anything, so `hide_deprecated` has nothing to
hide. **The single highest-value change in this whole document is about six lines:
parse `status`, map it to `TagDeprecated`/`TagBeta`, done.** Everything else below
is refinement on top of a flag that would then finally work.

Concretely, for the four providers a user is most likely to add:

| provider | records | `status: deprecated` |
|---|---|---|
| google | 41 | 4 |
| openai | 47 | 10 |
| nvidia | 98 | 2 |
| anthropic | 15 | 2 |

`TagBeta` is already declared and **never used anywhere** (`model.go:177`, the only
occurrence in the tree). The 53 `beta` + 1 `alpha` records would give it its first
job.

### 1.2 The second finding: "discontinued" is not the main noise — and my first predicate for it was wrong

Deprecation explains 4 of Gemini's 41 rows. The rest of the felt noise is models
that are perfectly current but cannot hold a conversation.

**The correction that matters.** The obvious rule is "keep models whose output
modalities include `text`". Measured against the real payload, that rule is
**wrong**, and it fails on exactly the model the user would complain about first:

```
gemini-embedding-001   in=[text]                        out=[text]   limit.output=1
gemini-embedding-2     in=[text,image,audio,video,pdf]  out=[text]   limit.output=1
gemini-3.5-flash       in=[text,image,video,audio,pdf]  out=[text]   limit.output=65536
```

Both embedding models declare `output: ["text"]`. A modality-only filter **keeps**
them. What separates them from a chat model is not the modality but
`limit.output = 1` and `temperature: false` — an embedding emits one vector, not a
turn.

I tested single-field predicates across all 6.106 records against a keyword-derived
notion of "non-conversational". None is usable alone:

| predicate | precision | recall |
|---|---|---|
| `output` lacks `text` | 0.25 | 0.20 |
| `temperature == false` | 0.07 | 0.40 |
| `temperature == false && !tool_call` | 0.49 | 0.35 |

**So the rule has to be a disjunction of three cheap, independently-justifiable
signals**, each of which carries its own reason string:

| signal | meaning | catches |
|---|---|---|
| `output` non-empty and lacks `text` | emits audio/video/image only | TTS, Veo, Imagen, Flux, Ideogram |
| `limit.output <= 1` | cannot emit a turn | embeddings, some image endpoints |
| `temperature == false && !tool_call && !structured_output` | not a sampled generator | embeddings, rerankers, safety guards, whisper |

Applied to Gemini's 41 records it flags exactly 9, and they are all correct:

```
gemini-2.5-flash-preview-tts       no-text-output
gemini-2.5-pro-preview-tts         no-text-output
gemini-3.1-flash-tts-preview       no-text-output
gemini-omni-flash-preview          no-text-output
veo-3.1-generate-preview           no-text-output
veo-3.1-fast-generate-preview      no-text-output
veo-3.1-lite-generate-preview      no-text-output
gemini-embedding-001               degenerate-output-limit
gemini-embedding-2                 degenerate-output-limit
```

Auditing the disjunction's apparent "false positives" showed my keyword baseline
was the thing at fault, not the predicate: `poe/google/imagen-4`,
`poe/ideogramai/ideogram-v2`, `nvidia/black-forest-labs/flux.1-dev` are all
genuinely non-conversational, they simply did not match my regex. Two entries do
deserve care — `poe/poetools/claude-code` and `poe/google/gemini-deep-research`
carry `limit.output = 0`, which is a **missing** value that Poe reports as zero, not
a real degenerate limit. Hence the rule below treats `0` as unknown and only
`limit.output == 1` as evidence.

### 1.3 The third finding: near-duplicates, and how many there really are

| provider | total | `-preview` twin of an existing GA id | dated twin of an undated id | `-latest` aliases | ids containing `preview` |
|---|---|---|---|---|---|
| google | 41 | 3 | 0 | 2 | 22 |
| openai | 47 | 0 | 3 | 3 | 0 |
| nvidia | 98 | 0 | 0 | 0 | 0 |
| anthropic | 15 | 0 | 4 | 0 | 0 |

Two things to read off this table.

**Duplicate shape is per-provider, not universal.** Google duplicates by
`-preview` (`gemini-3-pro-image` *and* `gemini-3-pro-image-preview`); Anthropic and
OpenAI duplicate by date stamp (`claude-sonnet-4-5-20250929` alongside
`claude-sonnet-4-5`); NVIDIA does neither, it is just genuinely large. A single
global rule cannot be tuned for all three, which is the strongest argument in this
document for **per-provider policy** — the exact thing the report asked for.

**And it proves a tempting default is unsafe.** 22 of Google's 41 ids contain
`preview`, but only 3 are superseded twins. The other 19 include
`gemini-3.1-pro-preview`, `gemini-3-flash-preview`, `gemini-3.1-flash-live-preview`
— current, wanted models. **A blanket "hide anything with `preview`" rule would
hide the best model Google offers today.** Superseding must be judged
*relationally* (hide `X-preview` only when `X` exists), never by name shape alone.

### 1.4 What the combined policy actually delivers

Non-chat + deprecated + superseded + dated-twin, with each model counted once
under its first matching reason:

| provider | total | shown | hidden | breakdown |
|---|---|---|---|---|
| google | 41 | **26** | 15 | 9 non-chat · 4 deprecated · 2 superseded |
| openai | 47 | **28** | 19 | 8 non-chat · 9 deprecated · 2 dated twins |
| nvidia | 98 | **72** | 26 | 24 non-chat · 2 deprecated |
| anthropic | 15 | **10** | 5 | 2 deprecated · 3 dated twins |

A 37% cut for Gemini and 40% for OpenAI, with **zero configuration typed by the
user** and no judgement call about model quality anywhere in it.

**Conclusion that shapes the design.** Three different problems are being felt as
one, and they need three different mechanisms:

| # | problem | right mechanism | wrong mechanism |
|---|---|---|---|
| 1 | non-conversational models | **capability signals** (§1.2's disjunction) — objective, no list to maintain | name matching (`*embedding*`) — breaks on the next naming fashion |
| 2 | deprecated / superseded | **metadata** (`status`) + **relational** id comparison | asking the user to hide them one by one |
| 3 | "current, valid, but not for me" | **the user's own list** — nothing but taste can decide this | any automatic rule |

Only problem 3 needs a per-user hide list. Problems 1 and 2 should be quiet by
default, out of the box — which is the difference between a feature and a chore.

### 1.5 A constraint the report contains but does not state

The user asked to *remove* models. The catalog cannot remove anything: existence is
defined by provider discovery (§4.3), and the next `--refresh` brings the model
straight back. So "remove" has to mean **hide from view**, persistently, and the
design has to be honest that the model is still there (§4.3's own rule: "hiding
them makes the fix impossible to discover"). `ishakat models clean` already taught
this lesson once — a provider deleted from `config.toml` kept reappearing from
cache, and the fix was a visible command, not a silent purge.

---

## 2. Design principles this has to obey

Taken from the existing contracts, not invented here. Each one kills at least one
otherwise-tempting design.

1. **Hiding is a view, never a deletion.** The cache keeps every discovered model.
   A hidden model stays resolvable by exact ref, so a saved session or a script
   that names it does not break. (Kills: pruning the cache file.)
2. **Say the number.** "26 shown · 15 hidden" is a footer line, not a secret. §4.4
   already shows a staleness strip for the same reason. (Kills: silent filtering.)
3. **Never hide what the user has used.** `merge.go:161` already refuses to hide a
   deprecated model when `UseCount > 0` or when it came from config
   (`Source.Has(SourceConfig)`). Every new rule inherits that carve-out unchanged.
4. **One resolver, one truth.** `/model gemini-embedding-2` must not say "not
   found" for a model the picker is merely hiding (§4.5 forbids bare "not found"
   anyway). Hidden models stay in the scoring pool at a penalty, and resolving one
   explicitly says so.
5. **Curation is a pure function.** It belongs in `internal/catalog`, which §6.1
   forbids from importing `net/http` or `lipgloss`. Table-testable, no terminal.
6. **Zero network on the critical path.** Curation runs on the already-loaded
   snapshot. It must not need a lookup it does not have.
7. **The config file the user wrote stays byte-identical.** The TUI must never
   rewrite `config.toml`. See §4.1 — this is the design's one non-obvious decision.
8. **40 columns.** Every screen below is drawn at 40 columns before it is drawn at 120.
9. **Ranking beats filtering when in doubt.** A folded group at the bottom of the
   list costs one line and loses nothing; a filter that guesses wrong loses a model.
10. **Unknown is never a reason to hide.** A gateway model with no models.dev match
    has no modalities, no limits and no status. It is shown. Silence is not evidence.

---

## 3. What to build: four layers, cheapest first

Each layer is independently shippable and independently useful. If only the first
two ever get built, the reported problem is solved.

```
Layer 0  metadata      make hide_deprecated actually work            ~6 lines
Layer 1  defaults      capability + supersede rules, on by default   no user action
Layer 2  interactive   hide/keep from the picker, one keystroke      the asked-for feature
Layer 3  settings      /config screen · per-provider screen          the second question
```

### Layer 0 — Read the metadata that already exists (bugfix)

`internal/catalog/modelsdev.go`, in `wireMDModelRaw`:

```go
type wireMDModelRaw struct {
    // …existing fields…
    Status      string `json:"status"`      // "deprecated" | "beta" | "alpha" | ""
    Temperature *bool  `json:"temperature"` // pointer: absent ≠ false (§1.2)
}
```

carried into `MDModel` by `digest`, and in `applyModelsDev`:

```go
switch strings.ToLower(md.Status) {
case "deprecated":    m.addTag(TagDeprecated)
case "beta", "alpha": m.addTag(TagBeta)
}
```

`Temperature` **must** be `*bool`. `false` is evidence of a non-sampled model
(§1.2); *absent* is no evidence at all, and 6 of the records I sampled omit it
entirely (`evroc/Qwen3-Embedding-8B` among them). A plain `bool` would silently
convert "unknown" into "not a chat model" and violate principle 10.

No new config, no new UI: `hide_deprecated = true` starts doing what its name has
always promised, and `penaltyDeprecated` (`resolve.go`) starts pushing the survivors
down the ranking instead of never firing.

**Closing criterion:** a models.dev fixture whose record carries
`"status": "deprecated"` produces a model tagged deprecated, and `Build` with
`HideDeprecated: true` drops it — unless `UseCount > 0` or it came from config.
A record with `"status": "beta"` gets `TagBeta`, which today no code path can
produce. A record with no `temperature` key leaves `Temperature == nil`.

### Layer 1 — Rules that are right for everybody, on by default

New pure file `internal/catalog/curate.go`. One policy type, one function:

```go
// Rules is the curation policy: which models are worth showing. The zero value
// shows everything, so a caller that does not care is never surprised.
type Rules struct {
    ChatOnly       bool     // drop models that cannot answer a chat turn (§1.2)
    HideDeprecated bool     // moved here from BuildInput
    HideSuperseded bool     // "X-preview" when "X" also exists
    HideDatedTwins bool     // "X-20250219" when "X" also exists
    HideLatest     bool     // "X-latest" aliases            (default OFF)
    Hide           []string // user globs, e.g. "*-tts*", "veo-*"
    Keep           []string // wins over every rule above, including ChatOnly
}

// Reason is why one model was dropped — carried so the interface can explain
// itself and `models --why` has something to print.
type Reason string

const (
    ReasonNonChatModality Reason = "no text output"      // §1.2 signal 1
    ReasonNonChatLimit    Reason = "output limit 1"      // §1.2 signal 2
    ReasonNonChatSampling Reason = "not a sampled model" // §1.2 signal 3
    ReasonDeprecated      Reason = "deprecated"
    ReasonSuperseded      Reason = "superseded"
    ReasonDatedTwin       Reason = "dated snapshot"
    ReasonLatestAlias     Reason = "latest alias"
    ReasonUserGlob        Reason = "hidden by you"
    ReasonUnhealthy       Reason = "failing"
)

type Hidden struct {
    Model  Model
    Reason Reason
}

// Curate partitions a snapshot. It never mutates cat and never deletes from the
// cache: the second return value is the complete audit trail.
func Curate(cat Catalog, r Rules) (kept Catalog, hidden []Hidden)
```

#### 1.1 `ChatOnly`, as corrected by the measurement

Three signals, OR'd, each reporting its own reason (§1.2 proved no single one is
sufficient):

```go
func nonChat(m Model) (Reason, bool) {
    // 1. Declared output modalities that do not include text at all.
    //    Empty modalities = unknown = keep (principle 10).
    if len(m.Modalities) > 0 && !slices.Contains(m.Modalities, "text") {
        return ReasonNonChatModality, true
    }
    // 2. An output limit of exactly 1 token cannot carry a turn. Zero is NOT
    //    evidence: Poe reports missing limits as 0 (§1.2), so 0 means unknown.
    if m.MaxOutput == 1 {
        return ReasonNonChatLimit, true
    }
    // 3. No sampling, no tools, no structured output: a scorer, not a generator.
    //    Requires Temperature to be explicitly false, never merely absent.
    if m.Temperature != nil && !*m.Temperature &&
        !m.Caps.Tools && !m.Caps.JSONSchema {
        return ReasonNonChatSampling, true
    }
    return "", false
}
```

This removes all 9 non-conversational Gemini entries, OpenAI's 3 embeddings and 5
image endpoints, and NVIDIA's 24 embed/rerank/guard/diffusion models — with no list
to maintain when Google ships `lyria-4`.

#### 1.2 The relational rules, which must not be name-shape rules

§1.3 measured why: 22 of Google's 41 ids contain `preview` and only 3 are
redundant. So both duplicate rules compare against the ids actually present in the
same provider, and hide nothing when the counterpart is absent.

```go
// HideSuperseded: hide "X-preview" only if "X" exists in the SAME provider.
// gemini-3.1-flash-image-preview → hidden (gemini-3.1-flash-image exists)
// gemini-3.1-pro-preview         → KEPT   (no gemini-3.1-pro; it is the only one)
//
// HideDatedTwins: reuse NormalizeID (modelsdev.go:311 already strips date
// suffixes via dateSuffix) and hide "X-<date>" only if undated "X" exists.
// claude-sonnet-4-5-20250929 → hidden (claude-sonnet-4-5 exists)
// deep-research-preview-04-2026 → KEPT  (no undated counterpart)
```

`HideLatest` is **off by default**. `gemini-flash-latest` is what some users
deliberately want — a moving target that never needs a config edit. Offered, not
chosen for them.

#### 1.3 Defaults, and the per-provider policy the report asked for

`internal/config/defaults.toml`:

```toml
[catalog.curate]
chat_only        = true    # models that cannot answer a chat turn are hidden
hide_deprecated  = true    # moved from [catalog]; kept as an alias one release
hide_superseded  = true    # "-preview" when the GA id also exists
hide_dated_twins = true    # "-20250219" when the undated id also exists
hide_latest      = false   # "-latest" aliases are a legitimate choice
hide             = []      # your own globs
keep             = []      # wins over everything above
```

§1.3 is the argument for per-provider rules: Google duplicates by `-preview`,
Anthropic and OpenAI by date stamp, NVIDIA not at all. One global setting cannot
be right for all three at once.

```toml
[[provider]]
id   = "google"
# …
hide = ["*-tts*", "veo-*", "lyria-*"]
keep = ["gemini-3.1-flash-image"]   # yes, I do want this one
```

Provider rules **merge** with the global ones: both `hide` lists apply, both `keep`
lists apply, `keep` always wins. No override semantics — "did the provider block
replace or extend the global list?" is a question a config format should never make
the user ask.

#### 1.4 Health-based demotion — half-built already, so finish it in the picker

Reading the code corrected this item too. `applyStats` **already** promotes a
failing model to `HealthCooling` at `FailStreak >= 3` (`merge.go:377`), and
`ishakat models` **already** prints it as a `[cooling]` badge
(`internal/app/models_cmd.go:320`). What is missing is that the *picker* ignores
`Health` entirely — the one place a user actually chooses a model.

So this is not new machinery: it is `ReasonUnhealthy` reusing an existing signal.
A model that 404s from a shim which lists it but does not serve it — a real
Gemini-via-OpenAI-shim shape — sorts to the bottom of the picker with `⚠ cooling`,
with no user action. Ranking-only by default; hiding only above a higher threshold,
because a transient outage must not evict a model permanently.

**Closing criterion for layer 1.** A table test over a fixture containing the 41
real Google ids asserts exactly which 15 are hidden and under which reason,
including these named cases:

- `gemini-embedding-2` → `ReasonNonChatLimit` (**not** kept, despite `output: [text]`)
- `gemini-3.1-pro-preview` → **kept** (contains `preview`, has no GA counterpart)
- `gemini-3.1-flash-image-preview` → `ReasonSuperseded` (GA twin exists)
- `veo-3.1-generate-preview` → `ReasonNonChatModality`
- a record with `limit.output == 0` → **kept** (0 is unknown, not degenerate)
- a record with no `temperature` key → **kept** (absent ≠ false)
- `Keep` overrides every reason; `UseCount > 0` is never hidden
- `Curate` is pure: same input, same output, no clock, no map-order dependence

### Layer 2 — The interactive part: hide and keep from the picker

This is the feature as literally requested: "some option so the user can remove
them from the `/model` panel if they want".

**One keystroke, in the place where the annoyance is felt.** Not a settings screen
you have to remember exists — the moment you notice `veo-3.1-generate-preview` in
your list is the moment you are looking at it in the picker, and that is where the
verb belongs.

```
1...5....0....5....0....5....0....5....0
 models · 26 shown · 15 hidden
 > gemini▊
 ───────────────────────────────────────
 GEMINI-DIRECT                        26
 ● gemini-3.1-pro-preview
     1.0M · $1.25/$10 · TV
 ▸ gemini-3.5-flash
     1.0M · $0.30/$2.50 · TV
   gemini-flash-latest
     1.0M · $0.30/$2.50 · TV
 ───────────────────────────────────────
 + 15 hidden · ctrl+h show
 ↑↓ move  enter use  ←→ collapse
 ctrl+x hide  ctrl+f filter:all  esc
```

Three keys, and the third is the one that makes the first safe:

| key | what it does |
|---|---|
| `ctrl+x` | hide the model under the cursor — appends its ref to the user's hide list, the row disappears, the counter increments |
| `ctrl+h` | toggle "show hidden": hidden rows return, dimmed, each tagged with its reason (`deprecated`, `no text output`, `superseded`, `hidden by you`) |
| `ctrl+x` on a hidden row | un-hide it — same key, reads as a toggle, and it is the escape hatch that makes pressing it a decision you cannot regret |

**Why a chord and not a bare letter.** My first draft of this document said `x`,
and reading the code before publishing proved that wrong. `updatePicker`'s
`default` branch (`internal/tui/picker.go:658`) forwards **every** key with
non-empty `Text` into the incremental search query:

```go
default:
    if key.Text != "" {
        m.picker = m.picker.typeText(key.Text)
    }
```

A bare `x` is therefore already taken — by typing the letter x into the search
box — and so is every other letter. Any new picker verb **must** be a chord, which
is also why the existing filter cycle is `ctrl+f` and not `f`. Worth recording
because it is exactly the kind of detail a design doc gets wrong and an
implementation then quietly "fixes" by breaking incremental search.

**Why `ctrl+h` and not another filter position.** `ctrl+f` cycles *what to include*
(all → free → tools → vision → favorites, `cycleFilter`); hidden-visibility is an
orthogonal axis — you may want "free models, including the hidden ones" — and
folding it into the same cycle would make the two interfere. It is also the honesty
guarantee of principle 2: the hidden ones are always one keystroke away, and the
footer always says how many.

Plus a one-line undo notice reusing `Root.slashNotice`, the same channel §4.6's
confirmation line already rides, so `evictOverflow` treats it like any other line:

```
 hid gemini-embedding-2 · ctrl+z undo
```

#### 2.1 The slash-command surface

For when the picker is not where you are, and for scripting:

| command | what it does |
|---|---|
| `/model hide <query>` | resolve with §4.5's resolver, hide the winner (ambiguous → picker prefiltered, never a bare "not found") |
| `/model keep <query>` | the inverse; also pins it against the automatic rules |
| `/models hidden` | list what is hidden and why |
| `/models reset` | drop every user hide, keep the automatic rules |

And on the command line, because `ishakat models` is already the debugging window
into the merge:

```
$ ishakat models --hidden               # only what is hidden, with reasons
$ ishakat models --why gemini-embedding-2
$ ishakat models --all                  # existing flag, now bypasses curation
```

`--why` is worth more than it looks. The failure mode of any filtering system is
"where did my model go", and a question that answers itself is what keeps a
mechanism from becoming a support burden:

```
$ ishakat models --why gemini-embedding-2
google/gemini-embedding-2
  discovered   yes (provider lists it)
  models.dev   matched (normalized)
  hidden by    catalog.curate.chat_only
  because      limit.output = 1 — cannot emit a conversational turn
               (note: its output modality IS text, so the modality
                check alone would not have caught it)
  still usable yes — `/model google/gemini-embedding-2` by exact ref
  to show it   add "gemini-embedding-2" to [catalog.curate].keep
```

#### 2.2 Where the hide list is stored, and why not in `config.toml`

**This is the design's one non-obvious decision, and it is load-bearing.**

`config.toml` is hand-written and heavily commented: `config.example.toml` is 208
lines of which 63 are comment lines explaining what each key does and what it
costs. `BurntSushi/toml`'s encoder cannot preserve any of them — `SaveProviderConnection`
(`internal/config/connection.go:41,85`) decodes into `map[string]any` and re-encodes
from it, and comments are simply not representable in a `map[string]any`. The loss
is structural, not a bug to be fixed.

That loss is survivable in `SaveProviderConnection` because it happens once, from
an explicit `ishakat provider add`. A key pressed casually inside a picker must not
have the same consequence: hide four models, and the config file the README tells
you to read has been stripped of the prose that made it readable.

So interactive state goes to its own machine-written file:

```
$XDG_STATE_HOME/ishakat/curation.json   # written by the TUI, never hand-edited
$XDG_CONFIG_HOME/ishakat/config.toml    # written by you, never by the TUI
```

```json
{
  "v": 1,
  "hidden": [
    { "ref": "google/gemini-embedding-2",        "at": "2026-08-05T18:22:04Z" },
    { "ref": "google/veo-3.1-generate-preview",  "at": "..." }
  ],
  "kept": [
    { "ref": "google/gemini-3.1-flash-image",    "at": "..." }
  ]
}
```

`$XDG_STATE_HOME` is the right home and the precedent already exists:
`xdg.StateHome()`, `xdg.StateDir()` and `xdg.ErrorFile()` are already there
(`internal/xdg/xdg.go:49,54,61`), and `last-error.json` is the same category of
thing — machine-written state, not configuration. Losing it is annoying, not
destructive, and it must not end up in a dotfiles repo where a hide performed on
one machine silently applies on another.

Precedence, weakest to strongest, matching how every other layer in this program
already resolves (`config.Load`'s layer order, §5.1):

```
built-in defaults
  < config.toml [catalog.curate]
    < [[provider]] hide/keep
      < curation.json          ← what you just pressed always wins
```

`keep` beats `hide` at the same level, always. "Show me this one" is a more
specific instruction than "hide that class of thing", and a user who explicitly
kept a model and then watched it vanish because a rule also matched would be right
to call it a bug.

**Migration path for the power user:** `/models export-curation` prints the TOML
block equivalent to the current state, to paste into `config.toml` by hand if they
want it versioned and shared. The program suggests; it does not write.

#### 2.3 Closing criteria for layer 2

- Hiding from the picker persists across restarts and does **not** touch
  `config.toml` — asserted byte-for-byte against a fixture, comments included.
- A hidden model is still resolvable by exact ref, and `/model` says it is hidden
  rather than failing (principle 4).
- `ctrl+h` round-trips; `ctrl+x` on a hidden row un-hides it; neither key reaches
  `typeText`, and typing `x` into the search box still filters.
- The footer count equals `len(hidden)` for every rule combination in the table.
- `curation.json` missing, empty, corrupt, or carrying a future `v` degrades to
  "nothing hidden" plus a note — never a startup failure. Same contract as
  `LoadCache` (`store.go`).
- Every screen renders at 40 columns with no broken line.

### Layer 3 — The settings surface: `/config`, and what a settings menu should *not* be

The second half of the report asks for "some agent configuration option, or a
settings menu, or per-provider configuration". Here is the recommendation, and it
includes one deliberate refusal.

**`/config` already exists as a table row with `KindUnimplemented`**
(`internal/slash/slash.go:112`, described as "config efectiva"), and §13 lists it as
owed by step 13 with a defined job: show the effective config with secrets
redacted. Build **that**, read-only, first:

```
1...5....0....5....0....5....0....5....0
 ── config · effective ────────────────
 layers   defaults + config.toml
          + credentials.toml
 model    google/gemini-3.5-flash
 theme    ascua
 stream   on
 ── catalog ───────────────────────────
 chat_only        true
 hide_deprecated  true
 hide_superseded  true
 hide             2 globs             ▸
 curated          26 shown · 15 hidden
 ── providers ─────────────────────────
 ● google        26/41  key ✓  ▸
 ○ openai                   —  key ✗  ▸
 ── tools ─────────────────────────────
 enabled  true      write  ask
 shell    ask       evolve suggest
 ──────────────────────────────────────
 enter drill in · e edit · esc back
```

Four things this screen does that a flat key-value dump does not:

1. **It says which layer a value came from.** The most common config complaint in
   any program with layered configuration is "I set it and it didn't take".
   `Config.Files` and `Config.EnvUsed` (`internal/config/schema.go:19,21`) already
   carry exactly this information and **nothing currently displays it**.
2. **It shows effective values, not the file.** `cat config.toml` is not a feature;
   the merge of defaults + user + project + credentials is.
3. **It redacts.** `api_key` renders as `key ✓` / `key ✗` — never a prefix, never a
   length. §13 requires it, and it is the reason this screen cannot be a raw dump.
4. **It shows the curation result inline** — `26 shown · 15 hidden` — which is what
   connects this screen back to the reported problem.

#### 3.1 The refusal: no general-purpose settings editor

**Do not build a screen that can edit every key in `config.toml`.** Recommended
against, with reasons:

- The schema is **123 `toml`-tagged keys across 17 structs** (counted in
  `internal/config/schema.go`), several of them security-relevant:
  `tools.permissions.shell`, `shell_deny`, `write_deny`, `egress`. A TUI toggle for
  those is a *worse* interface than a text file whose comments explain the
  consequences — and §19.8's whole threat model rests on those values being
  deliberately set, not flipped by someone browsing a menu.
- Round-tripping the file destroys the comments (§2.2, structural). The comments are
  the documentation. A settings menu that silently deletes the documentation is a
  net loss.
- It is a maintenance tax forever: every new key needs a widget, a validator and a
  40-column layout, or the menu becomes a lie by omission.

**Instead: `e` on a value opens `$EDITOR` at the right line, then re-validates.**
Zero widgets, zero schema duplication, and the user lands in the file *with* the
comments, which is where the real explanation lives. `config.Validate` already
produces good messages; on a failed re-validate, offer to reopen rather than saving
something broken.

**Then narrow, purpose-built editors only where the value is a list of nouns the
user cannot be expected to type from memory:** models (layer 2's picker), themes
(`ctrl+t`, phase 3), providers (§3.2), favorites. Those four are the whole set.

The distinguishing test: *is the value a choice from a list the program already
knows, or a number/mode/path only the user knows?* Build a picker for the first;
use `$EDITOR` for the second.

#### 3.2 Per-provider settings, which is where the report started

The report's own framing — "when I add the Gemini API, a pile of models loads" — is
provider-scoped, and §1.3 measured why per-provider policy is *necessary* rather
than merely nice: Google duplicates by `-preview`, Anthropic and OpenAI by date
stamp, NVIDIA by neither. Reached with `enter` on a provider row in `/config`, or
`/provider google`:

```
1...5....0....5....0....5....0....5....0
 ── google ─────────────────────
 name      Google Gemini
 kind      openai (compat shim)
 base_url  …googleapis.com/v1beta/openai
 key       ✓ verified 2026-08-05
 discover  on
 ── models ────────────────────────────
 discovered  41
 shown       26
 hidden      15
   no text output          7
   output limit 1          2
   deprecated              4
   superseded              2
 ── actions ───────────────────────────
 r  re-verify key
 d  re-discover models
 h  edit hide globs
 c  clear cached models
 !  disable provider
 ──────────────────────────────────────
 esc back
```

This is where "15 hidden" becomes explorable rather than mysterious, and it turns
four things that today require knowing a CLI incantation — `provider add` to
re-verify, `models --refresh` to re-discover, `models clean` to clear cache,
hand-editing `enabled = false` to disable — into one place. All four commands
already exist; this is a front door, not new machinery.

**`ishakat provider add gemini` should print the curation summary on success.** The
best moment to tell someone about noise control is the moment the noise would
otherwise appear:

```
✓ Google Gemini verified and enabled
  discovered 41 models · showing 26
  15 hidden: 9 not conversational (embeddings, TTS, video),
             4 deprecated, 2 superseded by a newer id
  see them with `ishakat models --hidden`
```

That is the entire reported problem, answered before it is asked.

---

## 4. The other features worth building, ranked by value per unit of work

The report's last question — "what other functionality can be built, in an
intelligent, functional, comfortable way?" — answered with the same discipline:
each item judged on what it costs, what already exists to build it on, and what it
lets a user stop doing by hand.

### 4.1 Build these — high value, foundations already present

**1 · Model roles instead of one active model.** The strongest idea in this list.
`[app]` already has three hardcoded names for the same concept —
`default_model`, `compact_model`, `fallback_model` (`schema.go:25-27`,
`defaults.toml:4-6`). Generalize to named roles:

```toml
[roles]
chat    = "google/gemini-3.5-flash"
coding  = "omniroute/anthropic/claude-sonnet-4-5"
cheap   = "google/gemini-3.1-flash-lite"
compact = "cheap"                              # roles may point at roles
vision  = "google/gemini-3.1-pro-preview"
```

`/model @coding` switches; `ctrl+o` rotates roles. That key is already bound
(`keys.go:32`, `ModelCycle`) and its help text already promises "rotar favoritos"
(`view.go:209`) — a role is just a favorite with a job. It composes with everything
in this document: curation shrinks the list to models actually worth assigning a
role to, and `CheckSwap` (`internal/engine/hotswap.go:142`) already validates every
switch. Cost: small — a config section plus a resolver stage that already has an
alias mechanism to imitate.

**2 · Auto-role selection, opt-in.** Once roles exist: a turn carrying an image
needs `vision`; `/compact` needs `compact`; a turn that would exceed the active
model's window could use the role whose window fits. `CheckSwap` already computes
that last conflict (`ContextTooSmall`, `hotswap.go:150`) — this is *acting* on what
it returns instead of only reporting it. Ship behind `[app] auto_role = false`: a
program that silently changes which model answers is a program you cannot reason
about, so it must be a choice.

**3 · Cost and latency ceilings as curation rules.** `Cost` and `P50Latency` are in
the record and only ever rendered. `[catalog.curate] max_cost_out = 20.0` hides
models above a price you would never knowingly pay, carrying its own reason. One
predicate, reusing all of layer 1's machinery.

**4 · `ishakat models --diff`.** Discovery already caches per provider with a
timestamp, so comparing the previous snapshot against the new one is nearly free.
"What changed since last week" (3 new, 1 newly deprecated, 2 gone) is exactly what a
user of a fast-moving provider wants, and it makes deprecation **visible at the
moment it happens** instead of being noticed months later as accumulated noise. This
is the long-term answer to the reported problem.

**5 · Surface `Health` in the picker** (§1.4). Already computed, already displayed by
`ishakat models`, ignored by the one screen where it would change a decision.

### 4.2 Build these later — good, but they need something else first

**6 · A "recent" section in `/model`.** Straight from `Stat.LastUsed`, which
`resolve.go:577` already reads for recency scoring. Cheap and pleasant — but it
matters much less once curation cuts 41 rows to 26, which is why it is here and not
above.

**7 · Provider-level regex filters** instead of globs. Strictly more powerful,
strictly worse to explain and to render at 40 columns. Only if globs demonstrably
fail in practice.

**8 · A sync path for `curation.json`.** Real once someone runs ishakat on a phone
and a laptop. Deliberately out of scope until then (it is state, not config — §2.2).

### 4.3 Do not build these

**9 · A general settings editor.** §3.1, with reasons: 123 keys, security-relevant
values, and structural comment loss.

**10 · Auto-hiding by name pattern as a shipped default** (`*preview*`, `*exp*`).
Measured in §1.3: 22 of Google's 41 ids contain `preview` and only 3 are redundant,
so this would hide `gemini-3.1-pro-preview` — among the most wanted models Google
offers today. Name shape is a bad proxy for quality, and a default that hides a good
model is far worse than a default that shows a bad one.

**11 · Pruning the cache to "remove" models.** §1.5. Existence comes from discovery;
the next refresh undoes it, and meanwhile a saved session that named the model
breaks.

**12 · Fetching each provider's deprecation schedule from its own docs.** Every
provider publishes it differently, in prose, and it would put network I/O on a path
that must not have it (principle 6). models.dev's `status` field is the ecosystem's
answer to this problem and it is **already in the payload we download** (§1.1).

---

## 5. Recommended order, and what each step is worth

| # | step | effort | what the user notices |
|---|---|---|---|
| 0 | parse models.dev `status` → `TagDeprecated`/`TagBeta` | ~6 lines | deprecated models disappear, because `hide_deprecated` finally works |
| 1 | `catalog.Curate` + `[catalog.curate]` defaults + `--hidden`/`--why` | small | Gemini 41 → 26 rows, OpenAI 47 → 28, nothing typed |
| 2 | `ctrl+x` / `ctrl+h` in the picker + `curation.json` + `/model hide\|keep` | medium | "I can remove the ones I don't want" — the actual request |
| 3 | `/config` read-only with layers + provider drill-in + `e` → `$EDITOR` | medium | one place to see what is set, where it came from, what got hidden |
| 4 | roles + `ctrl+o` rotation | medium | switching by job instead of by model id |

Steps 0 and 1 alone resolve the report. Step 2 is what was literally asked for.
Steps 3 and 4 answer "what else", and 4 is the one that changes how the product
*feels* rather than how tidy it looks.

**Where this belongs in the plan.** Steps 0–2 are Phase 2 finishing work — layer 0
is a bugfix against a shipped default and the picker already exists. Step 3
discharges step 13's existing `/config` obligation (today `KindUnimplemented`,
`slash.go:112`). Step 4 is the only genuinely new contract and should wait for a
§16 decision, because turning `[app]`'s three model keys into aliases of a role
table is a one-way door.

---

## 6. Open questions for the user

1. **`chat_only = true` by default?** It hides 9 of Gemini's 41 entries and 24 of
   NVIDIA's 98, with no ceremony. I am confident it is right — but it is the one
   default that hides *current, working* models, so it deserves an explicit yes.
2. **`ctrl+x` as the hide key.** It must be a chord: `updatePicker`'s default branch
   sends every printable key into incremental search (§ layer 2), so bare letters
   are unavailable. `ctrl+x` reads as "cut", which is closer to the truth than
   "delete". Confirm, or name another chord.
3. **Roles vs. favorites.** Roles largely subsume `[favorites]` (`schema.go:264`).
   Keep both (roles for jobs, favorites for `ctrl+o` rotation), or migrate favorites
   into roles and have one concept? Migrating is cleaner and breaks a config key.
4. **Should `keep` also pin against `hide_deprecated`?** As designed, yes: `keep`
   wins over every rule. The alternative — deprecation is non-negotiable — is
   defensible, but it would surprise someone who explicitly asked for that model.
5. **`limit.output == 1` as a hide signal.** It is what catches
   `gemini-embedding-2`, whose modalities claim `text` output (§1.2). I treat `0` as
   unknown because Poe reports missing limits as `0`. If a real chat model ever ships
   with `limit.output == 1`, this rule hides it — I judge that acceptable, since such
   a model could not answer anything anyway.
