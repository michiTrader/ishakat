# ishakat

A terminal CLI to talk to AI models. Go + Bubble Tea v2. One static binary,
no runtime, designed to be usable at 40 columns on a phone.

> **Status: Phase 2 (prototype), step 11 of 13 closed.**
> The headless pipeline (`ishakat -p "…"`), model catalog (`ishakat models`),
> and interactive TUI are wired end to end. The catalog resolves
> fuzzy/partial model names (`son45`, `haiku`, aliases) per §4.5, `/model`
> and the `ctrl+p` picker both use that same resolver to switch models
> mid-conversation, and a switch that would lose context, drop an
> unsupported block or hit a provider with no credential stops for
> confirmation (§4.6/§9.5) instead of failing silently on the next turn.
> See [What works today](#what-works-today) before filing a bug — `/compact`
> and `--resume` remain scheduled work.
>
> The single source of truth for the design and the step order is
> [`docs/PLAN.md`](docs/PLAN.md).

## What works today

| Area | State |
|------|-------|
| `ishakat config init/path/check` | ✅ works |
| `ishakat doctor` | ✅ works (DNS, paths, dialects, Termux detection, terminal capability + glyph sample) |
| `ishakat models [--json\|--refresh\|--all] [filter]` | ✅ works (discovery + cache + models.dev + embedded seed) |
| Fuzzy/alias/suffix model resolution (§4.5) | ✅ implemented in `internal/catalog.Resolve`, unit-tested, and wired into both `/model` and the `ctrl+p` picker |
| `ishakat -p "question"` (headless, streaming, JSONL session) | ✅ works |
| `ishakat` (interactive TUI) | ✅ streams real provider responses when a provider is configured and reachable |
| Slash commands: `/help`, `/clear`, `/new`, `/exit`, `/model`, plus autocomplete dropdown (§9.6) | ✅ implemented in `internal/slash` + `internal/tui`; the other §13 commands are listed and reply "not implemented yet" |
| Model picker (`ctrl+p`, §9.4) | ✅ implemented in `internal/tui.Picker`: fuzzy search, provider grouping, filters (all/free/tools/vision/favorites) |
| Hot swap confirmation (§4.6/§9.5) | ✅ `engine.CheckSwap` plus the `internal/tui` conflict dialog: compact/drop-oldest for a context conflict, switch-anyway for a capability warning, cancel-only when the destination has no credential |
| `/compact`, `--resume` | ❌ steps 12–13, not written yet |

The interactive mode now uses the same engine/provider pipeline as headless
mode. Without a configured or reachable provider, it fails the turn visibly
instead of pretending that an echoed prompt is a model response.

## Requirements

- Go 1.24 or newer (the toolchain declared in `go.mod` is 1.26.5 and Go will
  fetch it automatically on first build).
- An OpenAI-compatible endpoint. The default config points at OmniRoute on
  `http://localhost:20128/v1`, but any OpenAI-dialect base URL works.

## Build and run (desktop Linux/macOS)

```sh
git clone https://github.com/michiTrader/ishakat.git
cd ishakat
make build            # -> bin/ishakat
./bin/ishakat doctor
```

Other targets: `make test`, `make race`, `make check`, `make fmt`.

## Windows

The binary **must** be called `ishakat.exe`. On Windows a file without that
extension is not a program as far as the loader is concerned, so PowerShell
falls back to its file associations and opens it in an editor — which is
exactly what happens if you copy the output of a Linux build across, or run
`go build -o ishakat`. Nothing is wrong with the program in that case; the
name is.

```powershell
git clone https://github.com/michiTrader/ishakat.git
cd ishakat
go build -trimpath -ldflags "-s -w" -o bin\ishakat.exe .\cmd\ishakat
.\bin\ishakat.exe doctor
```

`make build` inside Git Bash, MSYS2 or a native `cmd`/PowerShell with GNU
Make installed adds the `.exe` itself. To cross-compile from Linux or macOS:

```sh
make windows          # -> bin/ishakat-windows-amd64.exe
make windows-arm64    # -> bin/ishakat-windows-arm64.exe
```

`go run ./cmd/ishakat` works on Windows regardless, because the temporary
binary the toolchain builds is named correctly for you.

### Which console

Both problems people hit on Windows are properties of the console, not of the
program, and `ishakat doctor` prints what was detected:

- **Windows Terminal** (the default on Windows 11, and installable from the
  Store on 10) renders 24-bit colour and the block characters the logo and
  the context bar are drawn with. This is the recommended host.
- **`powershell.exe` / `cmd.exe` opened from the Start menu** run in
  `conhost.exe`, whose output code page is the system's OEM one (cp437,
  cp850, …), not UTF-8. There the program deliberately drops to an
  ASCII-only look: a plain wordmark, `>` as the prompt, `#`/`.` in the
  context bar. That is not a downgrade in error — sending UTF-8 to that
  console produces `catÃ¡logo`, not a missing glyph.

Either decision can be overridden in `config.toml` when the guess is wrong:

```toml
[ui]
color  = "truecolor"   # auto | truecolor | 256 | 16 | none
glyphs = "unicode"     # auto | unicode | ascii
```

`ishakat doctor` is what tells you whether you need to. It prints both
decisions with the reason for each, the environment variables they were read
from, and then **the characters themselves**, taken from the interface's own
glyph table:

```
  cwd          ~/projects/ishakat
  color        truecolor      WT_SESSION is set (Windows Terminal)
  glyphs       unicode        WT_SESSION is set (Windows Terminal, UTF-8 and Cascadia Mono)
  signals      WT_SESSION=…  TERM_PROGRAM=…

  ▀█▀ █▀▀ █ █ ▄▀▄ █ ▄▀ ▄▀▄ ▀█▀
   █  ▀▀█ █▀█ █▀█ ██   █▀█  █
  ▄█▄ ▄▄█ █ █ █ █ █ ▀▄ █ █  █

  prompt  ›   marks  ▌ ●   cursor  █
  footer  • ▪   ·   context  │▓▓▓▓▓▓░░░░
  rule    ────────   scroll  ↑↓   spinner  ░▒▓█▓▒░▒▓
```

Read that sample and you know which of three things is happening:

- it draws cleanly → the guess was right, and whatever looks wrong is not a
  terminal-capability problem;
- **empty boxes or question marks** → the font lacks these characters, so the
  repertoire was guessed too generously: set `glyphs = "ascii"`;
- **garbled pairs** (`â–ˆ`, `catÃ¡logo`) → the console is decoding our UTF-8 as
  its own code page, which is the same fix: `glyphs = "ascii"`.

## Termux quickstart

This is the target platform, so it gets the full recipe.

### 1. Install Go

```sh
pkg update && pkg upgrade -y
pkg install -y golang git
go version            # needs 1.24+
```

### 2. Get the source and build

```sh
git clone https://github.com/michiTrader/ishakat.git
cd ishakat
go build -trimpath -ldflags "-s -w" -o $PREFIX/bin/ishakat ./cmd/ishakat
```

Building **inside Termux** is the recommended path: it produces a native
`android/arm64` binary with CGO available, which is what makes DNS
resolution behave (see below). The `make android` target in the Makefile is
for cross-compiling from a desktop with the Android NDK — you do not need it
if you build on the phone.

> First build downloads the Go toolchain plus the Charm dependencies and can
> take a few minutes. Later builds are seconds.

### 3. Check the environment

```sh
ishakat doctor
```

You want to see `termux true`, and the `probando DNS (models.dev)` line
ending in `OK`. If DNS fails, that is the known Android resolver problem:
Android has no `/etc/resolv.conf`, so Go's pure-Go resolver has nowhere to
read nameservers from. `internal/netfix` installs a shim that falls back to
the system DNS properties, and `doctor` reports which resolver ended up in
use. A build made **with** CGO (i.e. built inside Termux, which is the
default) uses the platform resolver and sidesteps the issue entirely.

### 4. Create the configuration

```sh
ishakat config init          # writes ~/.config/ishakat/config.toml with 0600
ishakat config path          # tells you where that is
nano $(ishakat config path)
```

At minimum, point a provider at your endpoint:

```toml
[[provider]]
id       = "omniroute"
name     = "OmniRoute"
kind     = "openai"
base_url = "http://localhost:20128/v1"   # or https://your-gateway/v1
api_key  = "$OMNIROUTE_API_KEY"
discover = true
enabled  = true
```

Then export the key (put it in `~/.bashrc` so it survives a new session):

```sh
export OMNIROUTE_API_KEY="sk-…"
```

Validate:

```sh
ishakat config check         # accepts the file or explains what is wrong
```

### 5. Talk to a model

```sh
ishakat -p "say hi in one line"
ishakat -p "explain this" -m omniroute/openai/gpt-5-mini
cat error.log | ishakat -p "what failed here?"
ishakat -p "list three ideas" --json | jq -r .delta
```

Headless mode activates automatically whenever stdin or stdout is not a TTY,
so pipes never try to draw an interface.

### 6. See the catalog

```sh
ishakat models               # offline, from cache or the embedded seed
ishakat models --refresh     # goes to the network once
ishakat models --json | jq .
ishakat models sonnet        # substring filter
```

With no cache and no network you still get a usable list: an embedded seed
catalog, marked as unverified.

## Troubleshooting

**`✗ provider: falta la clave de API for "omniroute"`**
The provider has no resolved credential. Export the environment variable
named in `api_key`, or write the key inline in the config file (which must
be 0600).

**`⚠ could not list the models of omniroute`**
Discovery could not reach `base_url`. Not fatal: the catalog falls back to
the models declared in the config plus the embedded seed. Check that your
gateway is actually listening (`curl $BASE_URL/models`).

**`⚠ seed catalog: nothing verified against the provider yet`**
Expected on a first run with no cache and no reachable provider. Run
`ishakat models --refresh` once a provider answers.

**The logo is unreadable / everything is blocks and question marks.**
Run `ishakat doctor` and read the glyph sample it prints (see
[Which console](#which-console)). Boxes mean the font is missing what we
chose, garbled pairs mean the console is decoding UTF-8 as its OEM code page;
either way `glyphs = "ascii"` under `[ui]` is the answer. On Windows, the
real fix is Windows Terminal.

**There is no colour / the banner is white.**
Same command: the `color` line names the variable that decided. A console
that sets no `TERM` and none of `WT_SESSION`, `COLORTERM`, `ConEmuANSI`,
`TERM_PROGRAM` or `ANSICON` makes no promises about colour, so none is sent.
`color = "truecolor"` under `[ui]` forces it.

**The interactive UI cannot reach a provider.**
The TUI now uses the real engine, so check the provider configuration and
endpoint. The failed turn remains visible with its error; use `ishakat -p`
to distinguish a TUI issue from a provider or network issue.

**On Termux, swiping up to scroll back snaps straight back to the input box.**
Termux's own terminal view auto-follows the cursor whenever the program
writes new output — while a reply streams, that is happening dozens of times
a second, so a manual scroll gets overridden almost immediately. This is not
specific to ishakat (the same report exists against other terminal-UI tools
run on Termux); there is no escape sequence a program can send to hold the
viewport or to learn that the user has scrolled. Termux 0.119.0+ ships a
**SCROLL** toggle for exactly this — add it to the extra-keys row (Termux
settings → Extra keys), tap it before scrolling back to freeze the viewport,
tap it again to resume auto-follow. Update Termux if you do not have the
button yet.

## Layout

```
cmd/ishakat        entry point, flags and subcommands
internal/xdg       paths and Termux detection
internal/config    TOML schema, merge, expansion, validation
internal/convo     agnostic conversation model + JSONL store
internal/provider  provider contract; openai/ speaks the dialect over SSE
internal/catalog   normalized model registry, cache, three-source merge
  └── fetch/       the only place in catalog/ allowed to touch the network
internal/theme     theme as data, Oklab gradients
internal/tui       Bubble Tea v2 view layer (no net/http, ever)
internal/app       the wiring: config → catalog → provider → tui/headless
```

The one boundary rule that orders everything: `internal/tui` must never end
up importing `net/http`, not even transitively. There is a test that fails
the build if it does.

## Contributing

Read `docs/PLAN.md` first, then `AGENTS.md`. One step at a time, in order,
no dependencies added without arguing them against the budget in §6.4.
