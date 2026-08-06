// warnings.go implements the "warnings by necessity" fix: config.Load
// collects a Warning for every enabled provider missing its credential
// (expand.go), which is correct for the audit commands (`config check`,
// `doctor`, `provider list` — they call config.Load directly and print
// cfg.Warnings unfiltered, on purpose, because a deliberate audit wants to
// see every configured provider's state). Startup (headless and the TUI) is
// different: a user who only configured "gemini-direct" got a warning about
// every other declared-but-unused provider's missing environment variable
// on every single run, for providers this run never asked to use — noise
// that once sent a debugging session chasing app.default_model/omniroute
// instead of the actual bug (docs/PLAN.md's 2026-08-06 audit entries).
//
// The fix keeps cfg.Warnings itself untouched — the audit commands must
// keep seeing everything — and instead filters what headless.go/app.go
// choose to print at startup: a provider-scoped warning only surfaces when
// it names a provider this run actually resolved to use.
package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
)

// providerWarningPrefix is the "Where" shape used for a warning that names
// one specific provider ("provider[omniroute]" or "provider[3]").
// Warnings that don't have this shape (schema, tools, credentials
// permissions, an ignored config key) are not about any one provider and
// must never be filtered out.
const providerWarningPrefix = "provider["

// providerWarningID extracts the provider id from a Warning.Where string
// shaped like "provider[<id>]", and reports whether it had that shape AND
// named an id (rather than a bare index).
//
// Two call sites build this Where string with different contents:
// expand.go's missing-credential warning uses the provider's actual id
// ("provider[omniroute]"), which is exactly the per-run relevance this
// filter exists to apply. validate.go's kind-unsupported warning instead
// uses the provider's position ("provider[2]") because Validate runs before
// ids are guaranteed unique — that one names a structural problem (an
// unsupported kind silently disabling a provider), not "you didn't set up a
// credential for something you're not using", so it must never be silenced
// by this filter regardless of which provider ends up running. A bare
// integer inside the brackets is the tell: treat those as unscoped.
func providerWarningID(where string) (string, bool) {
	if !strings.HasPrefix(where, providerWarningPrefix) || !strings.HasSuffix(where, "]") {
		return "", false
	}
	id := where[len(providerWarningPrefix) : len(where)-1]
	if _, err := strconv.Atoi(id); err == nil {
		return "", false
	}
	return id, true
}

// FilterWarningsForProviders keeps every warning that is not scoped to one
// specific provider, plus provider-scoped warnings whose provider id is one
// of wanted. Everything else — a provider-scoped warning about a provider
// this run never touches — is dropped from what gets printed, without
// mutating cfg.Warnings itself (callers that want the full audit, like
// `config check`, read cfg.Warnings directly and never call this).
func FilterWarningsForProviders(warns []config.Warning, wanted ...string) []config.Warning {
	want := make(map[string]bool, len(wanted))
	for _, id := range wanted {
		if id != "" {
			want[id] = true
		}
	}
	out := make([]config.Warning, 0, len(warns))
	for _, w := range warns {
		id, scoped := providerWarningID(w.Where)
		if !scoped || want[id] {
			out = append(out, w)
		}
	}
	return out
}

// WarningPrinter is P3's dedupe fix: app.go's own startup sequence prints
// up to eight independent warning strings (BuildEngine's own warn for the
// conversation model, BuildEngine's again for compact_model, one per
// cfg.Warnings entry, resume, session recorder, session lister…), and
// before this existed each fmt.Fprintf call had no way to know another
// call already printed the exact same line — which is precisely how the
// original bug report's two identical "missing $OMNIROUTE_API_KEY" lines
// happened: default_model and compact_model both resolved to omniroute and
// each produced its own, textually identical, provider warning.
//
// This is deliberately exact-string dedupe, not "one warning per
// provider" or any other semantic grouping: two different warnings that
// happen to mention the same provider (e.g. a missing credential and an
// unsupported kind) are both real information and must both be printed.
// It is only the literal repeat — the same sentence twice — that is noise.
type WarningPrinter struct {
	seen map[string]bool
}

// NewWarningPrinter returns a WarningPrinter with an empty seen-set, ready
// to have Warn called on it for the whole span of one process's startup.
func NewWarningPrinter() *WarningPrinter {
	return &WarningPrinter{seen: map[string]bool{}}
}

// Warn prints "⚠ <msg>\n" to w the first time msg is seen, and does nothing
// on every subsequent call with the same msg (including an empty msg,
// which every call site here already guards against with `if warn != ""`
// before calling — Warn treats "" as "nothing to say" too, so a caller
// doesn't have to keep that check in two places).
func (p *WarningPrinter) Warn(w io.Writer, msg string) {
	if msg == "" || p.seen[msg] {
		return
	}
	p.seen[msg] = true
	fmt.Fprintf(w, "⚠ %s\n", msg)
}
