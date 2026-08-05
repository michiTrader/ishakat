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
