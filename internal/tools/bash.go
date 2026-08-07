package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"time"
)

// maxBashOutputBytes mirrors maxReadFileBytes/engine's own
// max_tool_output_bytes default (§12bis): a command that prints 40 MB must
// not blow up the context, so combined stdout+stderr is capped here rather
// than relying solely on the truncation the agent loop applies later — a
// tool reports a result already bounded to a sane size, per Result's own
// doc comment ("a tool reports its full result and lets the one place that
// knows the ceiling apply it" is about the *second*, engine-side ceiling;
// this is the tool protecting itself and the process's own memory from an
// adversarial or merely chatty command before that ceiling is ever reached).
const maxBashOutputBytes = 32 << 10

// defaultBashTimeout bounds how long a single bash call may run before it is
// killed and reported as a timeout — a stuck `tail -f` or an interactive
// prompt waiting on stdin that will never arrive must not hang the whole
// turn forever. 120s matches the general "long enough for a real build step,
// short enough that a stuck command surfaces within one turn" balance the
// rest of the codebase strikes for network timeouts.
const defaultBashTimeout = 120 * time.Second

// maxBashTimeout is the ceiling on Args.TimeoutSeconds: a model cannot ask
// for an unbounded wait either.
const maxBashTimeout = 600 * time.Second

// denyPatterns are the "obvious shapes" §19.8's threat-model section commits
// to blocking outright rather than merely confirming: "The real mitigation
// is confirmation plus a deny-list of obvious shapes (rm -rf /, piping a
// fetched script to a shell, git push --force)." This is not a sandbox —
// bash can still delete anything the invoking user's own shell can delete —
// it is a narrow, deliberately small set of shapes that are almost never
// the intended command and very often either a catastrophic mistake or a
// prompt-injected instruction, so failing loudly beats running them.
//
// Every pattern here is matched against the raw command string, not a
// parsed AST — good enough to catch the shapes §19.8 names by name, not
// intended as a complete defense against a determined adversary rewriting
// the same operation another way. That is precisely why it is a deny-list,
// not a sandbox (§19.8's own "Other honest limits").
var denyPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{
		// rm -rf / or rm -rf /<root-level-only>, and the --no-preserve-root
		// escape hatch some rm builds require for the former to even run.
		re:     regexp.MustCompile(`\brm\s+(-\w*r\w*f\w*|-\w*f\w*r\w*|--recursive\s+--force|--force\s+--recursive)\s+(/|--no-preserve-root)`),
		reason: "rm -rf against the filesystem root",
	},
	{
		re:     regexp.MustCompile(`\bdd\s+.*\bof=/dev/(sd|nvme|hd|disk)`),
		reason: "dd writing directly to a raw block device",
	},
	{
		// A fetched script piped straight into a shell: curl|sh, wget -O-|bash,
		// etc. — never executed sight-unseen, per §19.8.
		re:     regexp.MustCompile(`\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh|python[0-9.]*)\b`),
		reason: "piping a fetched script directly into a shell",
	},
	{
		re:     regexp.MustCompile(`\bgit\s+push\b[^|;&]*(-f\b|--force\b)`),
		reason: "git push --force",
	},
	{
		re:     regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\};\s*:`),
		reason: "a fork bomb",
	},
	{
		re:     regexp.MustCompile(`>\s*/dev/(sd|nvme|hd|disk)`),
		reason: "redirecting output directly onto a raw block device",
	},
}

// bashArgs is bash's argument shape.
type bashArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// Bash is the bash core tool (§19.1): run a command through the user's
// shell and return its combined stdout+stderr.
//
// Danger: high, always — §19.5 rule #2's tier is inferred and fixed in code
// for a native tool, and nothing about bash's arguments can lower it: it can
// do anything the invoking user's own shell can do, including network
// access, process spawning, and irreversible changes (this file's own
// package-level doc comment). There is deliberately no "safe subset" —
// §19.8's "Other honest limits" states plainly that the real mitigation is
// confirmation (Step 16's permission gate, not this file) plus the small
// deny-list denyPatterns implements, never a sandbox.
type Bash struct{}

var _ Tool = Bash{}

func (Bash) Name() string   { return "bash" }
func (Bash) Danger() Danger { return DangerHigh }
func (Bash) Description() string {
	return "Run a shell command and return its combined stdout and stderr. Runs with the same permissions as the invoking user — there is no sandbox. A small deny-list blocks a few catastrophic command shapes outright (rm -rf /, piping a fetched script into a shell, git push --force); everything else that the shell would accept, this tool will run."
}

func (Bash) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"command": {
			Type:        "string",
			Description: "The shell command to run, exactly as it would be typed at a POSIX shell prompt.",
		},
		"timeout_seconds": {
			Type:        "integer",
			Description: "Maximum time to let the command run before it is killed and reported as timed out. Omit to use the default (120s); capped at 600s.",
		},
	}, "command")
}

func (Bash) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args bashArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("bash: invalid arguments: %w", err)
	}
	if args.Command == "" {
		return Result{}, fmt.Errorf("bash: command is required")
	}
	if args.TimeoutSeconds < 0 {
		return Result{}, fmt.Errorf("bash: timeout_seconds must not be negative")
	}

	if hit, reason := matchDenyPattern(args.Command); hit {
		return ErrorResult(fmt.Sprintf(
			"refused to run this command: it matches a blocked shape (%s). This is a deny-list of a few catastrophic command shapes, not a general safety review — see the tool's own description.", reason)), nil
	}

	timeout := defaultBashTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
		if timeout > maxBashTimeout {
			timeout = maxBashTimeout
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", args.Command)
	var buf boundedBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()

	out := buf.String()
	if buf.truncated {
		out += fmt.Sprintf("\n…[truncated: output ceiling of %d bytes reached]", maxBashOutputBytes)
	}

	if ctx.Err() != nil {
		// The caller's own context (not just this call's timeout) was
		// cancelled — e.g. the user hit esc. Surface that as a Go error so
		// the agent loop's cancellation path handles it, not as tool-error
		// data (§12bis: cancellation is not "the tool failed").
		return Result{}, ctx.Err()
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			msg := out
			if msg != "" {
				msg += "\n"
			}
			if runCtx.Err() != nil {
				msg += fmt.Sprintf("command timed out after %s and was killed", timeout)
			} else {
				msg += fmt.Sprintf("command exited with status %d", exitErr.ExitCode())
			}
			return ErrorResult(msg), nil
		}
		// The command could not even be started (shell not found, etc.) —
		// this is the "could not even be attempted" case Tool.Run's own doc
		// comment reserves for a Go error.
		return Result{}, fmt.Errorf("bash: could not run command: %w", runErr)
	}

	if out == "" {
		return OKResult("(command produced no output)"), nil
	}
	return OKResult(out), nil
}

// matchDenyPattern reports whether cmd matches one of denyPatterns, and if
// so, which reason to report. Matching stops at the first hit — the message
// only ever needs to name one blocking reason.
func matchDenyPattern(cmd string) (bool, string) {
	for _, p := range denyPatterns {
		if p.re.MatchString(cmd) {
			return true, p.reason
		}
	}
	return false, ""
}

// boundedBuffer wraps a bytes.Buffer and silently stops accepting writes
// past maxBashOutputBytes instead of growing without bound, recording that
// it did so via truncated. exec.Cmd's Stdout/Stderr just need an io.Writer,
// so this satisfies that without ever holding more than the ceiling in
// memory — unlike reading a command's full output first and truncating
// afterward, which is what read_file's simpler line-oriented ceiling can
// afford to do but a byte-stream command's output cannot.
//
// buf is a named field, not an embedded bytes.Buffer, deliberately: an
// embedded bytes.Buffer would promote its own ReadFrom method, and
// io.Copy — which is exactly what exec.Cmd uses internally to stream a
// command's stdout/stderr pipe into this writer — detects an io.ReaderFrom
// destination and calls ReadFrom directly instead of Write, silently
// bypassing this type's whole ceiling. Keeping buf unexported and
// unembedded means the only way to write into it is this type's own Write
// method.
type boundedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.truncated {
		return len(p), nil // report success so the command isn't disrupted by a write error
	}
	room := maxBashOutputBytes - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string { return b.buf.String() }
