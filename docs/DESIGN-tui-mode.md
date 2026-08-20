# Design · `[ui] tui_mode`, terminal detection, and the W0 test harness

**Status: design + dependency audit. Nothing here is implemented yet.**

This document exists because the owner asked for it explicitly, in these words:

> *"Antes de implementar cualquier dependencia nueva o modificar la estrategia
> de detección de terminales, documenta brevemente qué se añade, por qué es
> necesario y cómo afecta específicamente a la instalación/compilación en
> Termux."*

It answers exactly that, in that order, and then states the detection strategy
and the shared-state rule that the approved DECISION-1 requires. It is the gate
in front of `W0`; `W0` does not start until this is agreed.

Companion reading: `docs/ROADMAP-ux-2026-08-20.md` (the waves and the IDs used
below), `docs/PLAN.md` §3 (the decision being reopened), §6.1 (the import
boundaries, which constrain where each new file may live).

---

## 1. Dependency audit — the headline

**Nothing new is added. Not one module, not one build tag, not one C
dependency. `go.mod` is not touched by W0, W1 or W3.**

That is a finding, not an aspiration: it was checked against the module cache
before this document was written, and each claim below names the file that
proves it.

### 1.1 What `fullscreen` needs, and where it already exists

| Need | Already available in | Status in `go.mod` |
|---|---|---|
| Enter/leave the alternate screen | `tea.View.AltScreen` (a `bool` we already set to `false` in `internal/tui/view.go:View()`) | direct dep, in use |
| Own scrollback + viewport | our own `[]transcriptEntry` + `charm.land/bubbles/v2` | direct dep, in use |
| Reflow text to a new width | `ansi.Wrap` / `ansi.Hardwrap` (`github.com/charmbracelet/x/ansi`) — already used by `wrapText`, and §17 (2026-08-19) already verified it re-emits a background escape at the start of each wrapped line | direct dep, in use |
| Measure a styled line's real width | `lipgloss.Width` (already wrapped as `lipglossWidth` in `internal/tui/footer.go`) | direct dep, in use |
| Erase screen + scrollback | `ansi.EraseEntireDisplay` / `ansi.EraseEntireScrollback` (`ESC[2J` / `ESC[3J`) | direct dep, in use |
| Print the transcript on exit (DECISION-1b) | `tea.Println` — the same call `commitEntryCmd` already uses | direct dep, in use |

`AltScreen` being a plain field on `tea.View` is the load-bearing detail. In
Bubble Tea **v2** the alternate screen is not a separate renderer or a program
option — it is one boolean on the value `View()` already returns. So `regular`
and `fullscreen` differ by *what we return from one function*, not by which
library we call. That is what makes §4's "one logical state, two render
policies" rule enforceable rather than aspirational.

### 1.2 What the W0 harness needs, and why it is not a new dependency either

`W0` builds a test-side terminal emulator. The instinct is to reach for a
third-party VT emulator (`vt10x`, `tcell`'s `SimulationScreen`, `expect`-style
pty drivers). **That is rejected**, for reasons that are about Termux
specifically and not about taste:

- A pty-based harness needs a real pty, which means `CGO`, `os/exec` and a
  platform-specific path — and Termux is exactly where our CGO story is already
  delicate (§3 requires `CGO_ENABLED=1` + NDK for android/arm64 *only* for DNS,
  and we do not want a second reason).
- Any new module is a new thing to vendor, audit and keep in sync with
  `go1.26.5`, for test-only value.

Everything the harness needs is already in the tree:

| Harness need | Provided by | Evidence |
|---|---|---|
| Drive a **real** `tea.Program` without a TTY | `tea.WithInput(io.Reader)` + `tea.WithOutput(io.Writer)` + `tea.WithWindowSize(w,h)` + `tea.WithEnvironment([]string)` | `options.go:30,40,58,163`; upstream's own `tea_test.go` drives programs with two `bytes.Buffer`s (lines 71–81) — this is the supported pattern, not a hack |
| Parse the emitted byte stream into cells | `ansi.Parser` + `ansi.Handler` (`Print`, `Execute`, `HandleCsi`, `HandleEsc`) with `Cmd.Final()`/`Params.Param()` for CSI decoding | `parser.go:19,59,152`, `parser_handler.go:36`, `parser_decode.go:456` — a complete DEC-compatible state machine, in a package that is already a **direct** dependency |
| Deterministic time | `tea.WithFPS`, plus the existing `StreamIntervalMS`/`AnimFPS` seams | `options.go:142` |

So the harness is **our own ~300-line cell grid** (a `[][]cell`, a cursor, a
scrollback slice) fed by `ansi.Parser`. We write the grid semantics; we do not
write the escape-sequence parser, which is the part that would actually have
justified a dependency.

#### Verified, not assumed

The claim above is load-bearing enough that guessing would be irresponsible, so
it was **probed against the real pinned versions** before this document was
written (a disposable program under `/tmp`, not part of the repo). A real
`tea.Program` was run with `WithInput`/`WithOutput` on two `bytes.Buffer`s,
`WithWindowSize(20,6)` and `WithEnvironment(["TERM=xterm-256color"])`, rendering
a two-line inline view and quitting on a keypress fed through the input buffer.

Result — 91 bytes captured, and `ansi.Parser` decoded all of them:

```
"\x1b[?25l\x1b[?2004h\x1b[>4;2m\x1b[=1;1u\r\x1b[JHELLO\nWORLD\x1b[>4m\x1b[=0;1u\r\x1b[J\x1b[?25h\x1b[?2004l..."
Print   -> "HELLO", "WORLD"          (the frame's actual text)
Execute -> \r, \n                    (the control bytes the grid must act on)
CSI     -> 12 sequences, incl. CSI-J (erase display), ?25l/?25h (cursor hide/show)
```

Three facts this settles:

1. **Bubble Tea v2 renders normally to a non-TTY writer.** No pty needed, so no
   CGO and no platform-specific harness — the Termux concern in §1.2 is
   discharged by evidence rather than by argument.
2. **`ansi.Parser`'s `Handler` gives exactly the three callbacks a grid needs**
   (`Print`, `Execute`, `HandleCsi`), with `Cmd.Final()` returning the command
   byte — `J` for erase — so the grid can implement `ESC[2J`/`ESC[3J` semantics,
   which is B3's whole test.
3. **Input injection drives the model**, which is what makes the B1/B4
   regressions (open the `/` menu, then resize) scriptable.

The one thing the probe also shows, and which the grid must handle from day one:
the renderer emits `\r` + `ESC[J` and relies on **relative** cursor movement.
That is exactly the mechanism RC-3/RC-5 say goes wrong when the frame's real row
count differs from what the renderer predicted, so the grid must track the
cursor honestly (including terminal-side auto-wrap when a line exceeds the
width) rather than reconstructing the frame from the text. A grid that "fixes"
wrapping while parsing would hide the very bug it exists to catch.

### 1.3 Effect on Termux installation and compilation — the specific answer

| Axis | Effect |
|---|---|
| **New modules** | none. `go.mod`/`go.sum` unchanged by W0/W1/W3 |
| **CGO** | unchanged. §3's rule stands exactly as written: `CGO_ENABLED=1` + NDK for android/arm64 **for DNS only** (`internal/netfix`). No new CGO reason is introduced |
| **Binary size** | no new linked code. The grid emulator lives in `_test.go` files, so it is **not in the shipped binary at all** |
| **Build time on device** | unchanged; no new package in the non-test closure |
| **Runtime cost on Termux** | Termux defaults to `regular`, i.e. today's code path. A phone pays nothing for a mode it does not use |
| **New env vars required** | none. Detection reads variables that already exist (§3 below), and every one of them is optional |
| **`ishakat doctor`** | gains one line (`tui mode … because …`), reusing the existing `theme.Diagnosis` shape |

**One-line summary for the release notes:** on Termux, this change is a default
and a `doctor` line. Nothing to install, nothing new to compile.

### 1.4 The one thing that *would* need a new dependency, and is therefore not being done

Mouse-region text selection inside the alternate screen. That needs terminal-
specific protocol work with no portable answer. It stays out of scope (already
recorded in the roadmap's §5), and `/copy`, `ctrl+y` and DECISION-1b's
exit-transcript are the answer instead.

---

## 2. Why detection must be a first-class package, not an `if`

The owner's instruction is explicit:

> *"quiero que expliques/implementes una detección robusta del entorno: Termux,
> WSL/Ubuntu, Windows Terminal y terminales Linux/macOS normales. En particular,
> quiero que WSL pueda utilizar `fullscreen` cuando el terminal lo soporte. No
> asumas simplemente que 'WSL = regular'."*

That last sentence rules out the cheap implementation. "WSL = regular" is wrong
because **WSL is not a terminal** — it is a kernel interface. A WSL process is
usually displayed *by Windows Terminal*, which supports the alternate screen
perfectly well; it can also be displayed by legacy `conhost.exe`, by VS Code's
integrated terminal, by a `tmux` inside it, or by nothing at all in CI. Those
have different answers, and the kernel version string cannot tell them apart.

So the rule is: **detect the terminal, and treat the platform only as a
tie-breaker.** Concretely, two orthogonal questions, never conflated:

1. **Where are we running?** (`Platform`: Termux / WSL / Linux / macOS /
   Windows / unknown) — answered from OS + kernel + filesystem.
2. **What is drawing our output?** (`Host`: Windows Terminal / VS Code / iTerm2 /
   Apple Terminal / tmux / screen / conhost / generic-xterm / dumb / not-a-tty)
   — answered from the environment and the TTY probe.

`tui_mode` is decided from **(2)**, with **(1)** used only where (2) is genuinely
ambiguous. That is precisely how a WSL session inside Windows Terminal reaches
`fullscreen` while a WSL session in a bare `conhost` does not.

### 2.1 Where the code goes (§6.1 compliance)

New package **`internal/termenv`** — name deliberately not `terminal`, to avoid
confusion with `charmbracelet/x/term`.

- It imports **only** `os`, `runtime`, `strings`, `path/filepath`, `io`. No
  `net/http`, no `lipgloss`, no `bubbletea`, no `internal/config`.
- Rationale: `internal/tui` will consume it, and §6.1's
  `TestTUINoImportaHTTP` forbids HTTP in the TUI's transitive closure. A
  detection package that stayed pure is also one that `cmd/ishakat/doctor` and
  `internal/app` can call without dragging the TUI in.
- **A new `arch_test.go` guard ships with the package**, in the same style as
  `TestAskStaysPresentationFree`, so the purity is tested rather than promised.

`internal/xdg.IsTermux()` already exists (`xdg.go:110`) and is correct. It is
**reused, not duplicated** — `termenv` calls it, so there is exactly one
definition of "are we on Termux" in the tree.

### 2.2 The API shape, and why it mirrors `theme.Diagnose`

`internal/theme/diagnose.go` already solved this exact problem for colour and
glyphs, and its design note is worth quoting because it applies verbatim here:

> *"Two booleans printed without their justification would not have helped;
> 'ascii, because this Windows console sets neither WT_SESSION nor TERM' tells
> the user exactly which knob to turn."*

So `termenv` copies that shape rather than inventing one:

```go
// Pure. Every rule below is exercisable from a Linux test, including the
// Windows and Termux ones — the platforms the suite will never run on.
func DetectEnv(override string, goos string, env []string, tty bool, probe Probe) Detection

type Detection struct {
    Mode     Mode      // ModeRegular | ModeFullscreen
    Platform Platform
    Host     Host
    Reason   string    // one sentence, printable after the value
    Signals  []Signal  // what was read, in the order a human wants it
    Advice   []string  // the override that fixes it if it came out wrong
}

// Probe is the two filesystem/kernel facts detection cannot get from env.
// A struct of funcs so tests can supply them instead of needing a real
// WSL kernel or an Android filesystem.
type Probe struct {
    KernelRelease func() string // /proc/sys/kernel/osrelease
    Exists        func(string) bool
}
```

Injecting `Probe` is the difference between a rule that is tested and a rule
that is merely shipped. `theme.DiagnoseEnv` takes `goos` and `env` as parameters
for exactly this reason, and the comment on `DetectGlyphsEnv` states the
principle: *"a rule that can only be exercised by shipping it is not a rule."*

---

## 3. The detection table

Read top to bottom; **first match wins**. `Reason` is what `doctor` prints.

### 3.1 Overrides and hard stops (before any platform logic)

| # | Condition | Mode | Reason |
|---|---|---|---|
| 1 | `[ui] tui_mode` is `"regular"` or `"fullscreen"` | as set | `set by [ui] tui_mode = "…"` |
| 2 | `ISHAKAT_TUI_MODE` env var set to a valid value | as set | `set by ISHAKAT_TUI_MODE` |
| 3 | stdout is not a TTY | **regular** | `stdout is not a terminal` |
| 4 | `TERM=dumb` | **regular** | `TERM=dumb` |
| 5 | `TERM` unset on a non-Windows OS | **regular** | `no TERM in the environment` |
| 6 | `CI` set to a non-empty, non-`0` value | **regular** | `CI is set` |

Rules 3–6 are the honesty rules: the alternate screen in a pipe, a log file or a
CI job produces garbage, and a mode we cannot verify we can leave is a mode we
should not enter. Env-var override (rule 2) is above the TTY check *only* for
`regular`; forcing `fullscreen` without a TTY is refused with a warning, because
otherwise `ISHAKAT_TUI_MODE=fullscreen` in a CI config corrupts every log.

### 3.2 Platform

| Condition | `Platform` | Note |
|---|---|---|
| `xdg.IsTermux()` (`$PREFIX` contains `com.termux`, or `/data/data/com.termux/files/usr` exists) | `Termux` | reuses the existing definition |
| `goos == "android"` without the Termux markers | `Android` | some other Android shell |
| `/proc/sys/kernel/osrelease` contains `microsoft` or `WSL` (case-insensitive) | `WSL` | **the kernel, not the terminal** — see §2 |
| `goos == "darwin"` | `macOS` | |
| `goos == "windows"` | `Windows` | |
| `goos == "linux"` | `Linux` | |

`WSL_DISTRO_NAME` / `WSL_INTEROP` are read too, as corroborating signals, but
the kernel string is authoritative: those two are unset inside a `tmux` started
from a login shell that dropped them, and a detection that flips on a lost
variable is not robust.

### 3.3 Host — the question that actually decides the mode

| Condition | `Host` | Mode |
|---|---|---|
| `TMUX` set, or `TERM` starts with `screen`/`tmux` | `Multiplexer` | **fullscreen** — tmux/screen implement the alternate screen themselves and do it well |
| `WT_SESSION` set | `WindowsTerminal` | **fullscreen** |
| `TERM_PROGRAM` = `vscode` | `VSCode` | **fullscreen** |
| `TERM_PROGRAM` = `iTerm.app` | `ITerm2` | **fullscreen** |
| `TERM_PROGRAM` = `Apple_Terminal` | `AppleTerminal` | **fullscreen** |
| `TERM_PROGRAM` = `ghostty` / `WezTerm` / `alacritty` / `kitty`, or `TERM` = `xterm-ghostty`/`xterm-kitty`/`alacritty`/`wezterm` | `ModernTerminal` | **fullscreen** |
| `ConEmuANSI=on` or `ANSICON` set | `ConEmu` | **fullscreen** |
| `TERM` matches `xterm*`, `screen*`, `rxvt*`, `alacritty`, `foot*`, `st-*`, `vte*`, `linux`, `konsole*`, `gnome*` | `XtermLike` | **fullscreen**, *unless* `Platform == Termux` |
| `goos == "windows"`, `TERM` unset, no console hint | `LegacyConhost` | **regular** — this is the console host that also gets `GlyphsASCII` today |
| anything else | `Unknown` | **regular** — unknown is never a reason to take the user's screen |

**The Termux carve-out is one line, and it is the only place the platform
overrides the host.** Termux reports `TERM=xterm-256color`, so by host alone it
would land in `fullscreen`. It is forced to `regular` because DECISION-1 keeps
§3's promise on the platform §3 was written for: native scrolling with a finger
beats any reimplementation. A Termux user who disagrees sets
`[ui] tui_mode = "fullscreen"` and gets it — the mode is a setting, not a
verdict.

### 3.4 Worked examples (these become the table-driven test cases)

| Scenario | Platform | Host | Mode | Reason printed by `doctor` |
|---|---|---|---|---|
| Termux on a phone | `Termux` | `XtermLike` | **regular** | `Termux: native scrolling and selection are worth more than reflow` |
| **WSL Ubuntu in Windows Terminal** | `WSL` | `WindowsTerminal` | **fullscreen** | `WT_SESSION is set (Windows Terminal)` |
| **WSL Ubuntu in bare conhost** | `WSL` | `Unknown` | **regular** | `no terminal hint: assuming a legacy console` |
| WSL inside `tmux` | `WSL` | `Multiplexer` | **fullscreen** | `TMUX is set` |
| Windows native, Windows Terminal | `Windows` | `WindowsTerminal` | **fullscreen** | `WT_SESSION is set (Windows Terminal)` |
| Windows native, `cmd.exe` | `Windows` | `LegacyConhost` | **regular** | `a bare Windows console` |
| macOS iTerm2 | `macOS` | `ITerm2` | **fullscreen** | `TERM_PROGRAM=iTerm.app` |
| Ubuntu desktop, GNOME Terminal | `Linux` | `XtermLike` | **fullscreen** | `TERM=xterm-256color` |
| Linux TTY (no X) | `Linux` | `XtermLike` | **fullscreen** | `TERM=linux` |
| VS Code integrated terminal | any | `VSCode` | **fullscreen** | `TERM_PROGRAM=vscode` |
| `ishakat > out.txt` | any | n/a | **regular** | `stdout is not a terminal` |
| GitHub Actions | `Linux` | n/a | **regular** | `CI is set` |
| `TERM=dumb` | any | `Dumb` | **regular** | `TERM=dumb` |

The WSL rows are the point of the exercise: **the same WSL kernel produces two
different modes**, decided by the terminal that is drawing it. That is the
"no asumas que WSL = regular" requirement, discharged.

---

## 4. Two modes, one state — the rule that keeps this from becoming two renderers

The owner's constraint:

> *"La distinción `regular/fullscreen` debe ser una decisión de renderizado, no
> una excusa para mantener dos implementaciones frágiles. Ambas rutas deben
> compartir el mismo estado lógico de conversación y evitar duplicaciones,
> corrupción o pérdida de contenido al hacer resize."*

This is enforced structurally, in three rules that the W0 harness can check.

**Rule 1 — one state, and the mode may not be read while building it.**
`Root.transcript` is the single source of truth in both modes. The mode is
**not** allowed to influence what is *in* it, only how it is *emitted*. There is
no `if fullscreen` inside `submit`, `finishTurn`, `drainStream`,
`finishAgentTurn` or anything else that mutates conversation state. Rendering is
a pure function of `(state, mode, width, height)`.

**Rule 2 — the two modes differ at exactly one seam.** Today's renderer answers
two questions at once: *what does the frame look like* and *what has been
permanently committed to the terminal*. Those get split:

```
render(state, width, height) -> Frame     // shared. no mode awareness at all.
emit(Frame, mode) -> (bytes, committed)   // the ONLY mode-aware function.
```

- `regular`: `emit` prints overflow via `tea.Println` (unchanged, "printed means
  final") and returns a frame bounded by `height` (W1's invariant).
- `fullscreen`: `emit` sets `AltScreen = true` and draws a window over our own
  scrollback; nothing is committed to the real terminal until exit
  (DECISION-1b).

Every layout function (`ContentWidth`, `FooterSections`, `InputBox`,
`renderHelp`, wrapping, folding) lives above that seam and is therefore written
**once**. The mode cannot fork the layout, because the layout never sees it.

**Rule 3 — resize is the same operation in both modes: rebuild from state.**
No incremental patching of previously emitted output, ever. On
`WindowSizeMsg`, both modes recompute `Frame` from `transcript` at the new
width. The modes then differ only in what they can *repair*:

- `fullscreen` repairs everything, because it owns every visible cell.
- `regular` repairs the live region and leaves committed scrollback alone —
  §3's trade-off, now scoped to one mode instead of being global.

The corruption in **B4** is caused by emitting rows whose count the renderer
mis-predicted. W1's width invariant ("no emitted line exceeds the physical
width") applies **above** the seam, so it holds in both modes by construction —
`fullscreen` does not get its own copy of the bug, which was the explicit risk
called out in the roadmap's kill criterion for option (d).

### 4.1 What the harness asserts about the shared state

These are W0 test cases; they are what makes rules 1–3 real rather than
stylistic:

1. **Mode-invariant content.** Drive an identical script in both modes at the
   same size; the *logical* transcript must be byte-identical. Only emission
   differs.
2. **Resize idempotence.** `shrink → shrink → grow → grow` back to the starting
   size leaves a grid byte-identical to a fresh render of the same state — in
   **both** modes. This is B4's regression test and it is written as a shared
   test parameterised by mode.
3. **No content loss.** After any resize sequence, every committed entry is
   still findable (on the grid in `fullscreen`; in grid+scrollback in
   `regular`).
4. **One banner, ever.** Asserted in both modes (RC-5's "banner ×6").
5. **Bounded frame.** No emitted line wider than the grid; no frame taller than
   the grid.
6. **Exit transcript (DECISION-1b).** After quitting from `fullscreen`, the
   scrollback contains the whole conversation, in order, wrapped to the final
   width.

---

## 5. `[ui] tui_mode` — the config surface

```toml
[ui]
# "auto" (default) | "regular" | "fullscreen"
#
#   regular     inline. Committed lines are printed once and never repainted,
#               so the terminal's own scrolling and text selection keep working.
#               Cost: already-printed lines do not re-wrap when the width
#               changes. The default on Termux.
#   fullscreen  we own the screen: our own scrollback, reflow on resize, and a
#               real clear. On exit the whole conversation is printed to the
#               terminal so nothing is lost. The default on desktop terminals
#               that support it.
#   auto        detect (see `ishakat doctor`).
tui_mode = "auto"

# fullscreen only: print the conversation to the terminal on exit (DECISION-1b).
fullscreen_exit_transcript = true
```

`"auto"` is the default value rather than a platform string, so the shipped
`defaults.toml` stays identical on every platform and the decision is made once,
at start-up, by code that can be tested. `ishakat doctor` prints the resolved
mode with its reason and the override that changes it — the same contract
`theme.Diagnosis.Advice` already honours.

---

## 6. Sequence, and what "done" means

Approved order: **W0 → W1 → W3 → W2 → W4 → W5 → W6**. W3 moved ahead of W2, so
this document covers W0/W1/W3's shared foundation.

| Wave | Ships | Closes when |
|---|---|---|
| **W0** | `internal/termenv` + its purity guard + the grid harness + failing regressions for B1–B4 + RC-1 (`[keys]` chord validation, real double-press) + RC-2 (cursor) | the four bug cases fail for the documented reason, the chord audit passes, and `go test ./...` is green with the new tests marked as expected-fail where they pin an unfixed bug |
| **W1** | the `render`/`emit` seam; height + width invariants; one banner; real `ESC[3J`; footer reflow; F20 | B1, B2, B3 pass on the grid; harness assertions 4 and 5 pass in `regular` |
| **W3** | `fullscreen` emit path; `[ui] tui_mode` + detection wired; exit transcript; F14/F16 | harness assertions 1, 2, 3, 6 pass in **both** modes; B4 passes; existing `internal/tui` suite unchanged and green |

**Kill criterion for `fullscreen` (from the roadmap, restated so it is not
forgotten):** if it cannot reach parity on the existing `internal/tui` suite
plus the new grid tests within W3, it ships **disabled** behind the setting
rather than becoming a second half-correct renderer. Rule 2 above is what makes
that outcome cheap: `emit` is the only thing that would be reverted.

---

## 7. Open questions — none blocking

1. Should `Multiplexer` (tmux/screen) really default to `fullscreen`? It works,
   but a user who runs tmux *specifically* to keep scrollback may prefer
   `regular`. Proposal: `fullscreen`, since tmux's own copy-mode covers it.
2. `fullscreen_exit_transcript` on a very long session could print thousands of
   lines on exit. Proposal: print it all (a truncated transcript is worse than a
   long one), and revisit only if it is reported.

Neither blocks W0. Both are one-line changes to the table in §3.3 or the config
default in §5, and both are recorded here so the choice is visible rather than
accidental.
