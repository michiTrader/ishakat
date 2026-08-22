# Roadmap · the 2026-08-20 UX report, triaged and sequenced

**Status: W0 in progress.** The harness (#183) and B1–B4 regressions (#184) are in; RC-1 and RC-2 are implemented; B2b and B3 remain expected-fail pins (not silent). W1–W6 are still planning only. This document exists to
be argued with before a line of code is written, the same way
`docs/DESIGN-model-curation.md` was written to be argued with first.

It takes one long report — 4 rendering bugs plus ~20 feature requests, most of
them phrased as "Pi does this and I want it" — and turns it into: an inventory
with stable IDs, the root causes that are **already located in this codebase**
(with file/line evidence, so the next session does not re-investigate), three
architectural decisions that need the owner's explicit approval because they
contradict decisions marked **CLOSED** in `docs/PLAN.md` §3, and a wave-by-wave
implementation order with closing criteria.

The report's own framing is taken at face value and it changes the shape of the
answer: *"no quiero cambios superficiales, si hay que cambiar arquitectura o
cambiar cosas de raíz estoy dispuesto a pagar el costo"*. So the sequencing
below is **not** "cheapest first". It is "the thing that unblocks the most
other things first, even when that thing is a refactor".

Companion reading: `docs/PLAN.md` §3 (closed decisions — two of them are
reopened here, explicitly, per §0.2's rule), §7 (the Bubble Tea loop), §9 (the
wireframes and breakpoints), §12bis (the tool loop), §13 (the command surface),
§21 (autonomy/phase — steering touches it), and `docs/DESIGN-model-curation.md`
(already-designed, still-unimplemented catalog curation, which wave W4 finally
implements).

---

## 0. How to read this

- **B1–B4** are the reported bugs, in the report's own numbering.
- **F1–F20** are the feature requests, numbered by order of appearance.
- **RC-1…RC-9** are root causes located during triage. Each names the file and
  the mechanism. Several requests collapse onto one RC — that is the whole
  point of triaging before building.
- **DECISION-1…3** are the three things I will not decide alone.
- **W0–W6** are the waves. A wave is not a sprint: it is a set of changes that
  share one invariant, and it closes when that invariant is tested.

One rule inherited from `AGENTS.md` and worth restating because this backlog is
long: **no quick fixes.** Every item below is either done properly (the
invariant is stated, the test harness can see it, the closing criterion is
verifiable) or it is not done and says so.

---

## 1. Inventory

### 1.1 Bugs

| ID | Report | Symptom | Group | Severity |
|---|---|---|---|---|
| **B1** | bug 1 | The hardware cursor leaves the input box and gets drawn on the box's bottom border once the frame fills the terminal, and also when the `/` dropdown opens. Typing still works. | render | **P0** — the app looks broken while being functional |
| **B2** | bug 2 | A long answer "eats" earlier history: the user's own message stops being visible. | render | **P0** |
| **B3** | bug 3 | `/clear` and `ctrl+l` repaint a clean UI but the old session is still in the terminal's scrollback. It never truly clears. | render | **P1** |
| **B4** | bug 4 | Changing the terminal **width** duplicates, fragments and truncates the visible conversation (banner printed 6+ times, half-lines interleaved). Growing the window back does **not** repair it. | render | **P0** |

### 1.2 Feature requests

| ID | Report | Ask | Group | Priority |
|---|---|---|---|---|
| **F1** | installer | One-line installer with a branded UI (`irm …/install.ps1 \| iex` on PowerShell), then plain `ishakat` on PATH | distribution | P3 |
| **F2** | `/login` | In-session login that applies **hot** (no restart, no `--refresh`), and that offers omniroute among the choices | providers | P2 |
| **F3** | `/hotkeys` | A shortcuts screen; keep our ESC-dismissable overlay style (better than Pi's, which dirties the transcript); **and** overlays must open while the agent is working, without blocking input | UI surface | P1 |
| **F4** | `/settings` | Interactive settings editor: searchable list, per-item description, live apply, persist | config | P2 |
| **F5** | scoped-models | Enable/disable which models appear in `/model` and in `ctrl+p` cycling; `ctrl+s` saves; a disabled row is dimmed **entirely**, not just its `✓`/`✗` | catalog | P2 |
| **F6** | subagents | Keep/extend sub-agents; decide whether the surface is "extensions" | agent | P3 |
| **F7** | steering | The input must stay usable — and keep the cursor — while the agent works | loop | **P0** |
| **F8** | thinking | (a) reasoning streams **live** during a tool-enabled turn; (b) **one** command/chord expands & collapses reasoning *and* code blocks together, like Pi | loop + render | P1 |
| **F9** | `/effort` | Effort/thinking-level picker, a chord to cycle it, and a headless-equivalent flag | model surface | P2 |
| **F10** | `provider add` | Default to **not** verifying (`--verify` becomes the opt-in) | CLI | P1 (cheap) |
| **F11** | `/model` rows | `name [provider] TVR ✓` — short provider label (`google`, not `gemini-direct`), dimmed, no `models/`/`gemini/` prefix noise, price/detail for the highlighted row, "catalogs refreshed" notice; plus a CLI selector like `-m gemini-3.6-flash[omniroute]` | catalog | P2 |
| **F12** | `/name` | `/name [text]` names the session | session | P3 (cheap) |
| **F13** | steering/queue | Mid-turn steering messages (the running task takes them into account), `alt+enter` queues follow-ups, `alt+up` edits the queue | loop | P1 |
| **F14** | responsive | Use the whole terminal at any size; the footer must **reflow**, not silently drop information when narrow | render | P1 |
| **F15** | spinner | Prefer `⠴ Working...` over the current animation | cosmetic | P3 (cheap) |
| **F16** | input box | Keep our box style, but let it expand to the full width | render | P2 |
| **F17** | `/reload` | Reload keybindings, skills, prompts, themes, context files | config | P2 |
| **F18** | `@` | `@` autocompletes a path to reference a file | UI surface | P2 |
| **F19** | `ctrl+c` ×2 | Double `ctrl+c` must actually exit (it does not), plus an audit of every other advertised chord | keys | **P0** (cheap) |
| **F20** | breathing room | One blank row between the footer and the bottom edge of the terminal | render | P3 (cheap) |

Two asides from the report that are **already true today** and must not be
regressed while doing the above: `/help` is an overlay that ESC dismisses
instead of a transcript dump (the report calls this better than Pi), and the
input box's own `┌ │ └` style is liked as-is.

---

## 2. Root causes already located

These are findings, not guesses. Each one was read out of the current tree.

### RC-1 · `ctrl+c` ×2 is dead in any real run (explains **F19**)

**Fixed 2026-08-20 (W0).** `quit = "ctrl+c"` + `quit_repeat = 2`; `validateKeys`
rejects chords this build cannot produce; `handleGlobalKey` counts presses
against that number; help is generated from the loaded Map. The diagnosis
below is the record of what was wrong.

`internal/config/defaults.toml` used to ship `quit = "ctrl+c ctrl+c"`, and
`internal/tui/keys.go`'s `NewMap` copies that string verbatim into `Map.Quit`.
`handleGlobalKey` (`internal/tui/root.go:1431`) compares it against
`keyPressString(msg)`, which is `tea.KeyPressMsg.String()`
(`internal/tui/input.go:124`) — a **single chord**, i.e. `"ctrl+c"`. The
two-chord string can therefore never match, so with a real config loaded the
Quit branch is unreachable: no grace window, no exit, and `ctrl+c` also stops
cancelling a turn.

Why the suite is green: `newTestRoot` (`internal/tui/root_test.go:14`) builds
`Options` **without** `Cfg`, so `NewRoot` never calls `NewMap` and the tests
exercise `defaultMap` (`Quit: "ctrl+c"`), which works. The bug lives exactly in
the gap between the default map and the shipped defaults file.

Scope of the fix is bigger than one string: **every** `[keys]` default needs to
be validated as a chord this build can actually produce, and the double-press
semantic needs to be expressed as data (`quit = "ctrl+c"` + a repeat count, or
an explicit `quit_repeat = 2`) rather than by writing a chord twice.

### RC-2 · the cursor is deliberately unset while busy (explains half of **B1**)

**Fixed 2026-08-20 (W0).** `cursorFor` now returns the same offset cursor in
`ModeBusy` as in `ModeChat`; overlays still return `nil`. `updateBusy` still
swallows keystrokes — showing the cursor in the empty input is not
typing-while-busy (that is W2). The `/` overflow arithmetic (RC-3) is still
W1. The diagnosis below is the record of what was wrong.

`cursorFor` (`internal/tui/view.go`) used to return `nil` for every mode except
`ModeChat`. In Bubble Tea v2 a `tea.View` with no cursor does not move the
terminal's cursor — it stays wherever the last write left it, which is the end
of the frame: the input box's bottom border. That is precisely the reported
`└──❚────┘`, and it is also why the report says the cursor is "taken away" the
moment a task starts.

The `/` case has a second, independent cause: `cursorFor` offsets by
`headRows(m.head()) + headRows(m.slashMenuBlock())`, which is correct **only
while the whole frame fits on screen** (see RC-3). Once it does not, every
absolute row it computes is wrong by however many rows the terminal has
scrolled, and opening the dropdown is one of the cheapest ways to push the
frame past the bottom. That arithmetic is **not** this fix.

### RC-3 · there is no "the frame never exceeds the terminal" invariant (explains **B2**, the rest of **B1**)

`evictOverflow` (`internal/tui/root.go:2166`) is the only guard, and it has two
holes it documents itself:

- it stops at `keepInline = 2` (`root.go:2146`), so once the last two entries
  alone are taller than the terminal, nothing can shrink the frame;
- it evicts **whole transcript entries** only, and a live turn's own growing
  body is not an entry at all — a single long streaming answer overflows with
  nothing to evict.

Bubble Tea's inline renderer repaints by moving the cursor up as many rows as it
believes it drew. A frame taller than the screen breaks that belief permanently,
which is the mechanism `evictOverflow`'s own comment (`root.go:2154`) already
describes — it just was not given enough authority to enforce it. Missing
invariant, stated plainly:

> **The rendered frame is never taller than `lay.Height`.** Whatever does not
> fit is committed to scrollback or clipped by the live region, never emitted.

`B2`'s "my message disappeared" is this invariant being violated: the frame is
taller than the screen, so the top of it (the user's own bubble) is scrolled
away by the terminal, and the renderer's next repaint overwrites rather than
restores it.

### RC-4 · `/clear` clears the frame, never the scrollback (explains **B3**)

Both `ctrl+l` (`root.go:1473`) and `/clear` (`internal/tui/slashrun.go:89`)
drop `m.transcript`, reset `printedUpTo` and return `clearScreenCmd`
(`root.go:1910`), i.e. `tea.ClearScreen`. That erases the *visible screen*; it
does not touch the terminal's saved scrollback, and everything
`commitEntryCmd`/`tea.Println` printed lives exactly there — by design (§3:
"printed means final"). So `/clear` cannot remove it: the mechanism that makes
old turns scrollable is the same mechanism that makes them unclearable.

Two honest options, and the choice belongs in DECISION-1: emit an explicit
scrollback-erase (`ESC[3J`) alongside the screen clear, which is what the report
actually expects; or keep scrollback and rename the command's promise. Anything
in between is the current state, which the report correctly calls "parece que
borra pero no borra".

### RC-5 · width changes cannot be repaired under §3 (explains **B4**)

`docs/PLAN.md` §3 closes this: *"already-printed lines do not re-wrap when the
terminal width changes"*, reaffirmed 2026-08-03 with option (b) (reflow the live
region only) explicitly rejected. `updateDispatch`'s `WindowSizeMsg` branch
(`root.go:1258`) does the only thing that decision allows: rebuild `Layout`,
re-apply the input prefix/width, repaint the live region. Everything already
printed keeps its old wrapping.

But the report shows something worse than stale wrapping: **duplication that
survives growing the window back**, and a banner printed six times. That is the
renderer's row accounting drifting — a line emitted wider than the *physical*
terminal width gets wrapped by the terminal into 2 rows while the renderer
counted 1, so its next up-move lands mid-frame and it repaints over live text
instead of replacing it. Contributors currently in the tree:

- `ContentWidth()` (`internal/tui/layout.go:99`) caps prose at
  `ui.max_width = 100` in `BPAncho` but the frame is measured in
  `renderRaw`/`headRows` by counting `\n` only (`root.go:2174`) — nothing
  asserts "no emitted line is wider than `lay.Width`";
- the start-up banner is both re-rendered by `head()` while the transcript is
  empty **and** printed to real scrollback by `submit`/`startAgentTurn`
  (`agentturn.go:112`, and the whole `banner_clear_internal_test.go` file is
  archaeology about that interaction) — two producers of the same rows is
  exactly the shape of "banner ×6";
- an upstream inline-renderer erase bug was already hit once here and fixed by
  pinning `charm.land/bubbletea/v2@faf4dcf` (§17, 2026-08-18). The same class
  of defect is plausible again on width shrink, and this time we should be able
  to **prove** it locally rather than by reading upstream source (see W0).

**Conclusion:** B4 is not fixable as a bug. It is the accepted trade-off of a
CLOSED decision, and the report is a request to reopen that decision — which is
DECISION-1.

### RC-6 · the tool loop blocks, so the UI has nothing to show and nothing to accept (explains **F7**, **F8a**, **F13**, half of **F3**)

`RunAgentTurn` (`internal/engine/agentloop.go:174`) runs the whole
model→tools→model cycle and returns once, and its own doc comment says the
streaming path "will wrap each iteration's channel drain around its own
StreamBuf" — which was never built. `agentTurnCmd` (`internal/tui/agentturn.go`)
therefore hands Bubble Tea a single blocking `tea.Cmd`, and the UI's only live
signal for the whole turn is `tickAnim`. Consequences, all reported:

- reasoning appears in one lump at the end (`finishAgentTurn` reads
  `result.Reasoning`), never "being written";
- `updateBusy` (`root.go:1753`) swallows every key except Cancel, so the input
  is inert while working;
- there is no seam at which a steering message could be injected between
  iterations, so **F13 is not implementable at all** on today's contract.

Note the plain-streaming path (`m.eng.Start` + `StreamBuf` + `drainStream`,
`root.go:1919`) already does live reasoning correctly. The refactor is about
giving the agent path the same eventing, not about inventing it.

### RC-7 · the footer drops information instead of reflowing (explains **F14**)

`RenderFooter` (`internal/tui/footer.go:68`) joins the items and then **removes
items right-to-left** until the line fits. That is why the report's narrow
terminal lost `context/tokens/cost/cwd` entirely. Pi wraps instead. The fix is a
layout policy (wrap to N rows, then abbreviate, then drop) plus the same
"content adapts, information survives" rule applied to the help/hotkeys tables
the report praises (`renderHelp` uses a hard-coded `helpWidth = 38`,
`view.go:249`).

### RC-8 · the picker shows the wire id, not a human label (explains **F11**)

Rows are built from the catalog `Ref` — `provider_id` + `/` + the provider's own
model id — so Google's OpenAI-compat shim yields
`gemini-direct/models/gemini-3.6-flash`: two redundant path segments and a
provider id nobody would choose to read. Meanwhile `capsLabel`
(`internal/tui/picker.go:573`) already computes exactly the `TVR` badge the
report asks for, and `pickerMetaLine` already has context/cost/latency. So F11
is mostly *re-arranging information we already compute* plus one genuinely new
piece of data: a short, human provider label — which is DECISION-3.

### RC-9 · the curation design is written and unbuilt (explains **F5**, and the "discontinued models" noise behind it)

`docs/DESIGN-model-curation.md` is `Status: design only`, and its §1.1 finding
still holds: `hide_deprecated = true` is already the default and already
honoured, but nothing ever *tags* a model deprecated because
`internal/catalog/modelsdev.go` does not parse models.dev's `status` field. Six
lines fix the flag; the scoped-models UI the report wants sits on top of it. W4
implements that document rather than re-designing it.

---

## 3. The three decisions

> **STATUS 2026-08-20 — all three are ANSWERED by the owner.** Each decision
> below now carries an `ANSWERED` block recording what was decided, plus the
> constraints attached to it. Where a decision went **against** my
> recommendation, that is stated as such: the record is the decision, not the
> advice. DECISION-1 additionally acquired three binding engineering
> constraints, which are discharged in a separate gate document —
> **`docs/DESIGN-tui-mode.md`** — that must be agreed before any code moves.

### DECISION-1 · who owns the screen (reopens §3 "inline, no reflow")

**This is the decision that resolves B1, B2, B3, B4, F14, F16, F20 and the
"expand old code blocks" half of F8b.** It is marked CLOSED twice in §3, so per
§0.2 it is not changed on my own initiative — it is put here, with costs.

What §3 bought by choosing inline: native phone scrolling, native text
selection for copy/paste, and the invariant "printed means final", which
several code paths lean on. What it cost, now all four visible in one report:
no reflow on width change (B4), no real clear (B3), and a frame that can exceed
the screen and desynchronise the renderer (B1, B2).

| Option | What it is | Verdict |
|---|---|---|
| **(a) status quo** | keep inline; fix only what inline allows | ❌ leaves B4 permanently broken and B3 renamed rather than fixed |
| **(b) inline, hardened** | enforce the frame-height invariant (RC-3), a frame-width invariant (RC-5), a single banner producer, real `ESC[3J` on clear | ✅ **do this regardless** — it is W0/W1, and it is a prerequisite for everything else, but it does not give reflow |
| **(c) alt-screen + owned viewport** | take the screen, keep our own scrollback and scroll keys, reflow everything on resize | ⚠️ gives every reported behaviour, costs native scroll + mouse selection on Termux — the two things §3 refused to pay |
| **(d) dual mode** | `[ui] tui_mode = "regular" \| "fullscreen"`: `regular` = (b), `fullscreen` = (c); default by platform (Termux → regular, desktop → fullscreen) | ✅ **recommended** |

Recommendation: **(d)**, and note it is what Pi itself does — the report's own
`/settings` dump contains `TUI mode regular` and `Fullscreen exit output
transcript`. It keeps §3's promise on the platform §3 was written for (a phone),
gives the desktop the reflowing, clearable, fully-responsive UI the report is
asking for, and makes the trade-off a user-visible setting instead of a hidden
architectural constraint. Cost, stated honestly: two render paths to keep
correct, which is only affordable **because** W0 builds a test harness that can
see a real terminal grid (below). Without that harness, (d) is how you get two
sets of B4.

Kill criterion for (d): if `fullscreen` cannot reach parity on the existing
`internal/tui` suite plus the new grid tests within its wave, it ships disabled
behind the setting rather than becoming a second half-correct renderer.

> **ANSWERED 2026-08-20 — (d) dual mode APPROVED.** `regular` for Termux,
> `fullscreen` for desktop and other compatible terminals. §3's "inline, never
> alt-screen / no reflow" is therefore **reopened**, and the reopening notice is
> recorded in `PLAN.md` §3 so the CLOSED marker cannot be read in isolation.
>
> The approval came with three constraints, all binding, none optional:
>
> 1. **No hard-to-install dependencies, Termux above all.** Prefer what Ishakat
>    already uses; if `fullscreen` appears to need something new, first prove it
>    cannot be done with the existing set.
>    → *Discharged:* `docs/DESIGN-tui-mode.md` §1. Nothing new is added — not a
>    module, not a build tag, not a C dependency; `go.mod` is untouched by W0,
>    W1 and W3. The load-bearing fact is that `tea.View.AltScreen` is a plain
>    `bool` on the value `View()` returns, so `fullscreen` is a field, not a
>    renderer. pty- and VT-emulator dependencies were considered and **rejected
>    on Termux grounds specifically**, because a pty means CGO and CGO is exactly
>    what is already delicate there.
> 2. **Robust environment detection — and WSL must be able to reach
>    `fullscreen`.** "WSL = regular" is explicitly *not* an acceptable shortcut.
>    → *Discharged:* `DESIGN-tui-mode.md` §2–§3. Detection splits into two
>    orthogonal questions — `Platform` (where the process runs) and `Host` (what
>    actually draws our bytes) — and **mode is decided by `Host`**, with
>    `Platform` only as a tie-breaker. WSL under Windows Terminal or tmux gets
>    `fullscreen`; WSL under bare legacy conhost gets `regular`. 13 worked
>    scenarios are written out and become the table-driven tests.
> 3. **Not two fragile implementations.** `regular`/`fullscreen` is a rendering
>    decision; both paths share one logical conversation state, with no
>    duplication, corruption or content loss across resize.
>    → *Discharged:* `DESIGN-tui-mode.md` §4. One `render(state, w, h) -> Frame`
>    that is **forbidden to know the mode at all**, and a single
>    `emit(Frame, mode)` that is the only mode-aware function in the tree.
>    Resize never patches — it rebuilds from state. Six harness assertions pin
>    this, including shrink→shrink→grow→grow idempotence.
>
> The kill criterion above stands unchanged and now applies to the W3 gate.

**Sub-decision 1b:** on `fullscreen` exit, what does the terminal keep? Pi has
a setting for it (`transcript`). Proposal: print the whole session transcript to
real scrollback on exit, so a user who leaves the app still has the conversation
in their terminal — this preserves the practical benefit of inline (grep your
scrollback afterwards) without paying for it during the session.

> **ANSWERED 2026-08-20 — APPROVED.** On `fullscreen` exit the conversation is
> dumped to real scrollback. This makes the transcript dump a **correctness
> requirement, not a nicety**: it is the sixth harness assertion in
> `DESIGN-tui-mode.md` §4.1 (exit transcript must contain every committed turn,
> in order, exactly once), and it is part of the W3 gate rather than a follow-up
> polish item. Config key: `fullscreen_exit_transcript`, default `true`.

### DECISION-2 · the turn becomes an event stream (changes `RunAgentTurn`'s contract)

**Resolves F7, F8a, F13, and the "overlays while working" half of F3.**

Proposal: `RunAgentTurn` keeps its blocking form for headless/serve (it is the
§12bis closing criterion and internal/app depends on it), and grows a streaming
sibling that emits, per iteration: reasoning deltas, text deltas, tool-call
start/end, usage, phase changes, and an **injection point** where the runtime
may add a message before the next model call. The TUI uses only the streaming
form; the existing `StreamBuf` is the model for the buffering discipline
(coalesce at 50 ms, §7.3) so the UI does not get one message per token.

Three consequences the owner should agree to, because they are contract
changes and not refinements:

1. **`ModeBusy` stops being modal.** The input stays focused and editable, the
   cursor stays in the box (RC-2), overlays (`/help`, `/stats`, `/theme`,
   `/hotkeys`, `/settings`) open on top of a running turn, and `esc` keeps
   meaning cancel only while the input is empty — otherwise `esc` clears the
   editor, and cancellation moves to a chord that cannot be typed by accident
   mid-sentence. This is a keyboard-semantics change to a documented shortcut
   (§13), so it needs a yes.
2. **Steering is a real conversation event, not a UI trick.** A steering message
   is appended to the session history between iterations, is persisted to the
   JSONL like any other user message, and is shown in the transcript as one.
   It **never** widens permissions: a steering message cannot approve a pending
   tool call, cannot change autonomy, and cannot cancel a §21.4 invariant —
   §21's dialogs remain the only path for that. (Stated because "just send the
   text into the running loop" is the obvious implementation and it would
   quietly become an approval channel.)
3. **Queued follow-ups are session state.** `alt+enter` queues, `alt+up`
   re-opens the queue for editing, and the queue survives a turn boundary. Pi's
   own `Steering mode`/`Follow-up mode` settings (`one-at-a-time` in the
   report's dump) become two config keys rather than hard-coded behaviour.

> **ANSWERED 2026-08-20 — all three consequences APPROVED.**
>
> - **Consequence 1** (`ModeBusy` non-modal, `esc` re-scoped) is accepted as a
>   deliberate change to a documented shortcut (§13). The documentation change
>   ships **with** the behaviour change, not after it.
> - **Consequence 2** is accepted, and I am recording it here as a **security
>   property rather than a design preference**: steering can never approve a
>   pending tool call, widen autonomy, or cancel a §21.4 invariant. "Just send
>   the text into the running loop" is the obvious implementation and it would
>   silently create an approval channel, so this needs a test that *asserts the
>   negative* — a steering message arriving while a tool call is pending must
>   leave that call pending. Treated as a W2 gate, not a code-review note.
> - **Consequence 3** is accepted: the follow-up queue is session state and
>   survives turn boundaries, with `Steering mode`/`Follow-up mode` as config
>   keys.

### DECISION-3 · provider display identity (and the `-m model[provider]` syntax)

**Resolves F11's naming half, and touches F2.**

Today a provider has an `id` (`gemini-direct`) used as both the config key and
the display name (`internal/config/credentials.go:129` already carries a
separate `Name: "Google Gemini"`, unused by the picker). The report wants
`[google]`.

Proposal: add an optional short `label` to the provider schema, default it from
the preset (`gemini-direct → google`, `omniroute → omniroute`), render
`model-id [label] TVR ✓` with the label dimmed, and **keep `id` as the only
thing configs, refs and session files ever store** — a display label that leaks
into persisted refs would make every saved session ambiguous. The proposed CLI
syntax `-m gemini-3.6-flash[omniroute]` is then sugar resolved through the same
`catalog.Resolve` path as everything else, with `provider/model` still valid.

Open question for the owner: should the built-in preset `gemini-direct` be
**renamed** to `google` (a breaking change for anyone who already ran
`provider add`, needing a migration in `config.toml` + credentials), or should
it keep its id and only gain the label? My recommendation is label-only:
same visible result, zero migration risk.

> **ANSWERED 2026-08-20 — FULL RENAME, against my recommendation.** The owner's
> decision: `google` becomes the **real internal identifier**, not a display
> label. `gemini-direct` → `google` everywhere — migrations, references, tests,
> config, documentation. Rationale given: *"sigue siendo un proyecto interno y
> tenemos tiempo para hacerlo correctamente"*. My label-only recommendation is
> **overruled and withdrawn**; it stays above only as the record of what was
> considered.
>
> Because the id is what configs, refs and session files persist, a rename is a
> data migration and not a find-and-replace. The plan is therefore:
>
> 1. **Permanent read-time alias.** `gemini-direct` resolves to `google` on read,
>    forever, in `catalog.Resolve` and in every place a provider id is parsed.
>    This is not a deprecation window — old session JSONL files are historical
>    records and must stay readable indefinitely.
> 2. **One-shot write migration**, idempotent and backed up before touching
>    anything: `config.toml` provider tables and keys, the credentials store
>    (`internal/config/credentials.go:129`, `ID: "gemini-direct"`), and any
>    session-index `Ref` that is rewritten in the normal course of use.
> 3. **Session JSONL is never rewritten in place.** Historical turns keep the id
>    they were written with; the alias in (1) is what makes them resolve. A
>    migration that rewrites history is how you corrupt a transcript.
> 4. **The label from the original proposal still ships** — it is orthogonal, and
>    it is what makes `model-id [google] TVR ✓` render.
> 5. **A test asserts the alias, not just the rename**: loading a pre-rename
>    `config.toml` and a pre-rename session must both still work after migration.
>
> Scheduling note: this lands in **W5**, deliberately after the rendering and
> loop waves. A rename touching config, credentials and session refs is exactly
> the kind of change that should not be in flight while the renderer is being
> rebuilt.

---

## 4. Sequencing

Each wave states the invariant it establishes. **A wave does not close because
its features "work"; it closes because its invariant is under test.**

> **APPROVED ORDER 2026-08-20 — W0 → W1 → W3 → W2 → W4 → W5 → W6.**
> The owner chose the alternative offered in §6.5: **W3 before W2**, i.e. the
> visual corruption is relieved before the blocked input. Same total cost,
> different order of relief.
>
> Two gating rules were attached, and they are rules rather than intentions:
>
> - **W0 stays a test harness.** It does not change behaviour. Its entire job is
>   to make B1–B4 *fail for the documented reason* before anything is fixed. A
>   harness written after the fix proves nothing.
> - **No wave starts until the previous one meets its acceptance criteria and its
>   tests pass.** Waves are gates, not labels on a backlog.
>
> Consequence of W3-before-W2 worth stating out loud: **F8b's payoff is
> deferred.** Its "expand old code blocks" half needs the owned viewport from W3
> *and* the event stream from W2, so with this order the rendering half lands
> first and sits inert until W2 arrives. That is a real cost of the chosen order,
> accepted knowingly, not an oversight.
>
> One more gate precedes W0 entirely: the **pre-implementation documentation
> gate** demanded by the owner — anything that adds a dependency or changes
> terminal detection must first be documented as *what is added, why it is
> necessary, and how it affects Termux install/compile*. That document is
> `docs/DESIGN-tui-mode.md`. **W0 does not begin until it is agreed.**

### W0 · Ground truth: a harness that can see a terminal (prerequisite for everything)

Nothing in W1–W3 can be verified today. `newHeadlessRoot`/`playTurn` drive
`Update`/`View` directly and never construct a `tea.Program`, so no existing
test can observe what actually reaches a terminal — which is precisely why B1,
B2, B3 and B4 all shipped green, and why §17's 2026-08-18 entry had to diagnose
a renderer bug by reading upstream source.

- Build a test-side terminal emulator (a cell grid: writes, wraps, cursor
  moves, `ESC[2J`/`ESC[3J`, scroll region) and a harness that runs a real
  `tea.Program` against it.
- Regression cases that must fail **before** any fix lands: frame taller than
  the grid keeps the input box on the last rows (B1); the user's message is
  still on the grid after a long answer (B2); after `/clear` the grid **and**
  its scrollback are empty (B3); shrink→shrink→grow→grow leaves a grid
  byte-identical to a fresh render of the same state (B4); the banner appears
  exactly once, ever.
- Fix RC-1 (`[keys]` chord validation + an explicit repeat-count representation
  for double-press) and audit every chord advertised in `renderHelp` against
  the map that is actually loaded — the report's "y claro los comandos que
  hagan falta" is exactly this audit. **Done 2026-08-20:** `quit_repeat`,
  `validateKeys`, press counting, help generated from the Map. B2b and B3
  are expected-fail pins, not silent, and are not part of this fix.
- Fix RC-2 (the cursor always resolves to a real position inside the input).
  **Done 2026-08-20:** `cursorFor` returns the offset cursor in `ModeBusy` as
  well as `ModeChat`; overlays still hide it; `updateBusy` still swallows
  keys. Typing while busy is W2, not this change. B2b and B3 stay expected-fail
  pins.

**Closes:** F19, half of B1. **Invariant:** every render claim in this document
is falsifiable by a test that looks at a grid.

### W1 · The frame is bounded and the screen is ours to clear

- Enforce RC-3's height invariant: the live region clips (with an explicit
  "…N rows above" affordance), `keepInline` stops being a floor that can be
  violated, and the live turn is subject to the same bound as the transcript.
- Enforce RC-5's width invariant: no emitted line exceeds the physical width.
- One banner producer, not two.
- Real clear (`ESC[3J`) for `ctrl+l` and `/clear`, per DECISION-1.
- **F20** (one blank row above the bottom edge) and the footer's reflow policy
  (RC-7) land here, because both are frame-geometry rules and W0's harness is
  what proves they hold at every width.

**Closes:** B1, B2, B3, F20, part of F14.

> **Status (2026-08-20): W1 CLOSED, all five items landed.** Part 1: real
> clear (`ESC[3J` via `wipeScrollbackCmd`/`clearAndWipeCmd`, `root.go`) for
> `ctrl+l`, `/clear` and `/new` — closes **B3**.
> `TestB3ClearAlsoClearsScrollback` promoted from a deferred pin to a hard
> assertion. Part 2: RC-3's height invariant (closes B1/B2), RC-5's width
> invariant, the one-banner-producer fix, and F20 (one blank row above the
> bottom edge). Part 3: RC-7's footer reflow policy —
> `RenderFooter` (`internal/tui/footer.go`) now wraps items into
> `lay.FooterSections()` rows first, abbreviates (reusing path.go's own
> `truncateRunes`/`ShortenPath`) any item that still does not fit, and only
> drops items right-to-left as a last resort when neither wrapping nor
> abbreviating a whole subset fits — closing the rest of **F14**. A
> BPEstrecho terminal (40-59 columns, the project's own most common real
> case) now keeps every configured footer item visible, wrapped across two
> rows, instead of losing `context/tokens/cost/cwd` outright. See
> `docs/PLAN.md` §17, 2026-08-20 "W1 (part 1)", "W1 (part 2)" and "W1 (part
> 3, RC-7, closes the wave)". All acceptance criteria for the wave now pass
> together, per this document's own "no wave starts/closes piecemeal" rule.

### W2 · The turn stops blocking

Implements DECISION-2. Order inside the wave matters: eventing first, then the
UI affordances that depend on it.

1. Streaming agent-turn API in `internal/engine` (blocking form preserved for
   headless/serve, which is §12bis's closing criterion).
2. TUI consumes events: live reasoning during tool-enabled turns (**F8a**),
   phase/footer updates from real events rather than inferred ones.
3. Non-modal `ModeBusy`: typing, `/`-commands and overlays while working
   (**F7**, **F3**).
4. Steering + follow-up queue with their two config keys (**F13**).
5. One toggle that folds/unfolds reasoning **and** code together (**F8b**),
   replacing today's `ctrl+r`-folds-code-only. Its limitation is honest and
   inherited: in `regular` (inline) mode it cannot touch what is already in
   real scrollback; in `fullscreen` it can, which is one of DECISION-1's
   concrete payoffs.

**Closes:** F7, F8, F13, F3; unblocks F9.

> **Status (2026-08-22): W2 CLOSED, all five items landed.** Part 1:
> streaming agent-turn API in `internal/engine` (PR #195, `56fe847`). Part 2:
> the TUI consumes real events — live reasoning during tool-enabled turns
> and phase/footer updates from real events, not inferred ones (**F8a**,
> PR #196, `71026bc`). Part 3: non-modal `ModeBusy` — typing, `/`-commands
> and overlays stay reachable while the agent works (**F7**, **F3**,
> PR #197, `2e61894`). Part 4: the steering + follow-up queue with its two
> config keys, `alt+enter`/`alt+up` (**F13**, PR #198 scaffolding `dd86cbe`
> + PR #199 implementation `9ebe0fd`), including the TUI-side negative-
> security test for DECISION-2 consequence 2
> (`TestQueueSteeringCannotResolvePendingToolApproval`). Part 5: the unified
> fold/unfold toggle — `Root.foldCode`/ctrl+r now collapses a bubble's
> reasoning preview (`reasoningFoldSummary`, mirroring `foldSummary`'s own
> one-line shape for code) in the same call that already folds its fenced
> code blocks, replacing "ctrl+r folds code only" with **F8b**'s own text,
> "one toggle that folds/unfolds reasoning and code together" — without
> adding new state: it is still the single global bool §5's "deliberately
> not in any wave" note insists on, now reaching one more kind of content.
> `ui.reasoning`'s "off" stays authoritative in both directions — folding
> never reveals a hidden reasoning stream. The `regular`-mode "cannot touch
> committed scrollback" limitation and `fullscreen`'s DECISION-1 payoff both
> fall out unchanged, for free, because this reuses the exact same
> `foldCode` bool and `commitEntryCmd`/`evictOverflow` machinery already
> wired through both modes — no mode-aware branching was needed anywhere in
> this slice. See `docs/PLAN.md` §17, 2026-08-21/22 entries for parts 1-5
> (part 5, "W2 closing," has the full detail). All acceptance criteria for
> the wave now pass together, per this document's own "no wave starts/
> closes piecemeal" rule.

### W3 · Screen ownership and true responsiveness

Implements DECISION-1(d): `fullscreen` render path with owned scrollback,
reflow on resize, exit-transcript, and the `[ui] tui_mode` setting with a
platform default.

- **B4** closes here and only here.
- **F14** completes (help/hotkeys tables and every overlay reflow to the real
  width instead of `helpWidth = 38`).
- **F16** (input box expands to full width) closes here.
- `/hotkeys` (**F3**) ships as its own overlay, generated from the loaded
  keymap so it can never drift from reality again (RC-1's second lesson).

**Closes:** B4, F14, F16, F3 (dedicated-overlay half only — see part 5 status
below; F3's "opens while the agent works" half is W2's, not W3's).

> **Status (2026-08-21): W3 CLOSED — parts 1-6 landed.**
> Part 6 closes the wave: `emit`'s `fullscreen` branch now sets
> `AltScreen = true` (the documented stub from part 3 is gone),
> `Root.ExitTranscript` implements DECISION-1b's exit-transcript flush
> (called by `internal/app.Run` only after `p.Run()` returns — bubbletea
> v2's renderer flushes bytes on an independent ticker decoupled from
> Update/View, so that is the one point guaranteed to be safe), and
> `evictOverflow` no-ops entirely in `fullscreen` (no real scrollback to
> evict into; unbounded in-memory transcript growth is the accepted
> trade-off, mirroring `docs/DESIGN-tui-mode.md` §7's "print it all... 
> revisit only if reported" call for the same reason). §4.1's two
> previously-unclaimed harness assertions now have dedicated tests:
> assertion 3 (`TestB4bFullscreenLosesNoContentAcrossAResizeCycle`) and
> assertion 6 (`TestFullscreenExitFlushesTheWholeTranscriptToScrollback`,
> plus `TestFullscreenExitTranscriptDisabledPrintsNothing` for the config
> gate) — both in `internal/tui/renderemit_internal_test.go`, both driven
> through the real `testterm` harness (a new `Session.FinalModel()` was
> added so a test can call `ExitTranscript()` on the actual model
> `p.Run()` returned, the same seam `internal/app.Run` uses, rather than a
> second Root built by hand). All six §4.1 assertions now pass in both
> modes; F14/F16/F3 (dedicated-overlay half) closed in parts 4-5. See
> `docs/PLAN.md` §17, 2026-08-21 "W3 (part 6, closing)".
>
> **Status (2026-08-20): W3 in progress, not closed — parts 1-5 landed.**
> Part 5: F3's own row has two halves — "a shortcuts screen" and "overlays
> must open while the agent is working, without blocking input" — and the
> roadmap names F3 under *both* W2's closes list (the non-modal half) and
> W3's (the dedicated-overlay half). Since the approved order runs W3 before
> W2, only the dedicated-overlay half can close here. It does: `/hotkeys`
> (`ModeHotkeys`, `internal/tui/hotkeys.go`) is now its own overlay — its own
> `slash.Kind`, `Mode`, renderer, and `Commands` row — reachable the same way
> `/help` already is, dismissed by any key. Its renderer deliberately does
> not duplicate `/help`'s shortcut list: both call the same
> `m.helpShortcuts()`, so the two screens can never drift from each other or
> from the loaded keymap. F3's "opens while working" half remains open,
> waiting on W2's non-modal `ModeBusy` eventing. See `docs/PLAN.md` §17,
> 2026-08-20 "W3 (part 5)".
> Part 4: F14 closes — `helpHeading`'s `const helpWidth = 38` is gone;
> `renderHelp` now measures `m.lay.ContentWidth()` once and hands it to both
> headings, the same call every other overlay already makes for its own
> rule lines. F16 was investigated and found **already closed** by earlier,
> unattributed work: `SetInputWidth`/`InputBox` (`internal/tui/input.go`)
> have been `lay.ContentWidth()`-driven, not a literal, since the commit
> that introduced them. F3 was investigated and deliberately **not**
> started this slice: it needs a genuinely new `slash.Kind`, `Mode`,
> renderer and dropdown row — a self-contained diff of its own, not a
> byproduct of F14's width fix — and is left for its own slice rather than
> risk landing half-wired. See `docs/PLAN.md` §17, 2026-08-20 "W3 (part 4)".
> `internal/termenv` (the detection package `docs/DESIGN-tui-mode.md` §2
> specifies) was already built and merged as part of W0, fully passing its
> own 12+-scenario table from §3.4. Part 1 wired it into config and
> `ishakat doctor`: new `[ui] tui_mode`/`fullscreen_exit_transcript` config
> keys (`internal/config/schema.go`, `defaults.toml`, both `example.toml`
> copies), and `ishakat doctor` now prints a `tui_mode` line — value, reason,
> its own signals, advice — the same contract `theme.Diagnosis` already
> honours for `color`/`glyphs`. Part 2 threaded the resolved `Mode` the rest
> of the way to the running interface: `internal/app.Run` now calls
> `termenv.Detect` once (reusing the same TTY answer `NoTTY` already
> computed) and hands the result into a new `tui.Options.TUIMode` field,
> wired into `Root.tuiMode` and surfaced by `/debug`'s `[terminal]` section
> for in-session confirmation. Part 3 built §4 Rule 2's render/emit seam:
> `internal/tui/view.go` now has a `Frame` type and an `emit(Frame, mode,
> cursor) tea.View` function — the only function in the package with a
> `termenv.Mode` parameter — with `View()` shrunk to
> `emit(Frame{Content: m.render()}, m.tuiMode, m.cursorFor())`. `render()`
> itself needed no change: it was already mode-blind, which the new
> `TestRenderIsModeInvariant` now confirms by asserting byte-identical output
> from a regular/fullscreen pair driven through an identical script.
> **Deliberately not done yet:** `emit`'s `fullscreen` branch still falls
> through to `AltScreen = false` — a documented stub, not an oversight.
> Turning it into `true` needs its own scrollback/viewport, a resize-repair
> strategy (Rule 3), and DECISION-1b's exit-transcript flush, none of which
> exist yet; doing it without those would silently break the exit-transcript
> promise the moment eviction met the alternate screen's own non-persistent
> buffer — exactly the "second half-correct renderer" the wave's own kill
> criterion forbids. F14/F16/F3 are untouched. See `docs/PLAN.md` §17,
> 2026-08-20 "W3 (part 3)" (and parts 2/1 below it) for the full detail.

### W4 · The model surface stops being noise

Implements `docs/DESIGN-model-curation.md`, starting with its own §1.1
six-liner, plus DECISION-3.

- Parse models.dev `status` → real `TagDeprecated`/`TagBeta`, so the
  already-default `hide_deprecated` finally does something (RC-9).
- **F5** scoped-models: per-model enable/disable, `ctrl+s` persists,
  disabled rows dimmed in full, and the set drives both `/model` and `ctrl+p`
  cycling.
- **F11** row format `id [label] TVR ✓` + detail (price/context) for the
  highlighted row + the "catalogs refreshed / N pending" notice + the
  `-m id[provider]` CLI form.
- **F10** invert `provider add` verification (`--no-verify` behaviour becomes
  the default; `--verify` opts in) — cheap, and it is in this wave because it
  is the same code path as the catalog/credential work.
- **F2** `/login` hot-apply and omniroute in the selection list (the hot-swap
  machinery already exists: `engineFor`/`EngineFactory`, `root.go`).

**Closes:** F5, F10, F11, F2, F9's model-side needs.

### W5 · Configuration and command surface

- **F4** `/settings`: searchable, described, live-applying editor over the
  existing `config` schema — which means the schema needs per-key metadata
  (label, help text, kind, allowed values) instead of a hand-maintained UI.
  That metadata is the actual work here; the overlay is the easy half.
- **F17** `/reload` (keymap, skills, prompts, themes, context files) — cheap
  once the metadata exists, and it is the honest fallback for anything
  `/settings` cannot hot-apply.
- **F9** `/effort` + a cycle chord + the headless flag.
- **F12** `/name`, **F15** the `⠴ Working...` spinner, **F18** `@` path
  completion (the `path.go`/`suggest.go` machinery is already there).

**Closes:** F4, F17, F9, F12, F15, F18.

### W6 · Distribution and sub-agents

- **F1** the branded one-line installers (`install.ps1` for PowerShell
  alongside the existing `install.sh`), plus PATH verification, and the "just
  type `ishakat`" first-run experience.
- **F6** sub-agents: audit what `internal/tools/dispatch.go` already does
  against §21.11's display promises, and decide whether the user-facing surface
  is "extensions" or stays tool-shaped. This is a design question, so it gets a
  companion document rather than a wave item.

---

## 5. Deliberately not in any wave

- **Mouse-based text selection inside `fullscreen`.** It is what alt-screen
  costs; `/copy`, `ctrl+y` and the exit-transcript are the answer.
- **Reflowing real scrollback in `regular` mode.** Physically impossible; §3's
  trade-off survives on that path, now as a documented mode rather than a
  global constraint.
- **Per-block fold memory across a session.** §17 (2026-08-18) already chose a
  single global toggle with reasons; F8b extends *what* it folds, not *how much
  state* it keeps.
- **Autonomy on `shift+tab`.** §21.16 decision 4 defers it pending real Termux
  measurement; F9's chord is about *effort/thinking level*, which is a
  different axis, and it must not be wired to autonomy by accident.

---

## 6. What I need from you before W1 — all answered (2026-08-20)

1. **DECISION-1:** approve dual mode (d) — and confirm `fullscreen` as the
   desktop default, `regular` on Termux. If you would rather go straight to
   alt-screen everywhere (c), say so; it is less work than (d), it just gives
   up native scrolling on the phone.
2. **DECISION-1b:** on exiting `fullscreen`, print the session transcript to
   the terminal? (my recommendation: yes)
3. **DECISION-2:** approve the three contract consequences — non-modal busy
   mode with `esc` re-scoped, steering as a persisted message that can never
   approve anything, and the follow-up queue as session state.
4. **DECISION-3:** label-only (`gemini-direct` keeps its id, displays as
   `google`) — or a real rename with a migration?
5. **Wave order:** W0→W1→W2→W3 is my recommendation (correctness and the
   harness first, then the loop, then the renderer). The alternative, if the
   visual corruption bothers you more than the blocked input, is
   W0→W1→W3→W2 — same total cost, different order of relief.

---

### 6.1 The answers

| # | Question | Answer | Notes |
|---|---|---|---|
| 1 | DECISION-1 dual mode | **APPROVED (d)** | `regular` on Termux, `fullscreen` on desktop/compatible. §3 "inline, no reflow" is formally reopened. Three constraints attached — see the ANSWERED block and `docs/DESIGN-tui-mode.md`. |
| 2 | DECISION-1b exit transcript | **APPROVED — yes** | Dump the conversation to real scrollback on `fullscreen` exit. Promoted from nicety to a W3 gate assertion. |
| 3 | DECISION-2 three consequences | **ALL THREE APPROVED** | Consequence 2 is recorded as a security property with a negative-assertion test, not a design preference. |
| 4 | DECISION-3 label vs rename | **FULL RENAME — against my recommendation** | `google` becomes the real internal id. Permanent read-time alias for `gemini-direct`; one-shot idempotent write migration; session JSONL never rewritten in place. Lands in W5. |
| 5 | Wave order | **W0→W1→W3→W2→W4→W5→W6** | The alternative, W3 before W2. Waves are gates. W0 must stay a pure harness. F8b's payoff is knowingly deferred. |

**Extra constraint the owner added, not in the original five:** a mandatory
pre-implementation documentation gate for any new dependency or any change to
terminal detection — stating what is added, why, and the specific effect on
Termux install/compile. Answered by `docs/DESIGN-tui-mode.md`, whose headline is
that **nothing new is added at all**. No wave, W0 included, starts until that
document is agreed.
