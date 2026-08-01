# ishakat

A terminal CLI to talk to AI models. Go + Bubble Tea v2. One static binary,
no runtime, designed to be usable at 40 columns on a phone.

> **Status: Phase 2 (prototype), step 7 of 13 closed.**
> The headless pipeline (`ishakat -p "…"`) and the model catalog
> (`ishakat models`) are real and wired end to end, and the catalog now
> resolves fuzzy/partial model names (`son45`, `haiku`, aliases) per §4.5 —
> that resolver isn't wired into headless `-m` or a picker yet, both are
> later steps. The interactive TUI is still the step-3 mannequin: it draws
> the final layout but it echoes your input instead of calling a model,
> because the engine is not connected to it until step 8. See
> [What works today](#what-works-today) before filing a bug — most of the
> "it looks raw" is scheduled, not broken.
>
> The single source of truth for the design and the step order is
> [`docs/PLAN.md`](docs/PLAN.md).

## What works today

| Area | State |
|------|-------|
| `ishakat config init/path/check` | ✅ works |
| `ishakat doctor` | ✅ works (DNS, paths, dialects, Termux detection) |
| `ishakat models [--json\|--refresh\|--all] [filter]` | ✅ works (discovery + cache + models.dev + embedded seed) |
| Fuzzy/alias/suffix model resolution (§4.5) | ✅ implemented in `internal/catalog.Resolve`, unit-tested; not yet wired into `-m` or a picker (steps 8/10) |
| `ishakat -p "question"` (headless, streaming, JSONL session) | ✅ works |
| `ishakat` (interactive TUI) | ⚠️ **mannequin** — real layout, echoes your text, no network |
| `/model`, model picker, hot swap, `/compact`, `--resume` | ❌ steps 8–13, not written yet |

The interactive mode being a mannequin is deliberate: step 3 built the TUI
early "as a visual reward" (see §12 of the plan), and step 8 is what
replaces the echo with the real engine. Until then, **use headless mode to
actually talk to a model**.

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

**The interactive UI just repeats what I type.**
Working as currently designed — that is the step-3 mannequin. Use
`ishakat -p` until step 8 lands.

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
