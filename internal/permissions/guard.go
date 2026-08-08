// Package permissions applies the runtime tool authorization policy.
package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/MichiTrader/ishakat/internal/config"
)

// Tier describes the possible impact of running a tool.
type Tier uint8

const (
	Low Tier = iota
	Medium
	High
)

// Request is the immutable description shown to an approval UI.
type Request struct {
	Name      string
	Arguments json.RawMessage
	Tier      Tier
}

// Decision is the response supplied by a person reviewing a request.
type Decision struct {
	Allow        bool
	AllowSession bool
}

// Reviewer obtains an explicit approval. Implementations may present a TUI
// overlay, read an interactive terminal, or reject when no human is present.
type Reviewer interface {
	Review(context.Context, Request) (Decision, error)
}

// ErrDenied reports that a configured or explicit permission boundary refused
// the tool request. Its message is safe to return to the model as tool output.
var ErrDenied = errors.New("tool permission denied")

// Guard applies configured hard denies, policy modes, optional session grants,
// and an injected reviewer. It is safe for sequential or concurrent callers.
type Guard struct {
	permissions config.Permissions
	yolo        bool
	reviewer    Reviewer

	mu      sync.Mutex
	session map[string]struct{}
}

// New creates a guard for one application session. yolo changes only ask into
// allow for file writes and shell commands; it never overrides hard denies.
func New(permissions config.Permissions, yolo bool, reviewer Reviewer) *Guard {
	return &Guard{
		permissions: permissions,
		yolo:        yolo,
		reviewer:    reviewer,
		session:     make(map[string]struct{}),
	}
}

// Authorize permits a request or returns ErrDenied. A high-tier request cannot
// receive a session grant, even if the reviewer asks for one.
func (g *Guard) Authorize(ctx context.Context, name string, arguments json.RawMessage) error {
	req := Request{Name: name, Arguments: clone(arguments), Tier: tierFor(name)}
	if reason := g.hardDeny(req); reason != "" {
		return fmt.Errorf("%w: %s", ErrDenied, reason)
	}

	mode := g.mode(req)
	if mode == "deny" {
		return fmt.Errorf("%w: %s is disabled by configuration", ErrDenied, req.Name)
	}
	if g.yolo && req.Tier == Medium {
		return nil
	}
	if mode == "allow" || req.Tier == Low {
		return nil
	}

	key := requestKey(req)
	if req.Tier == Medium && g.hasSessionGrant(key) {
		return nil
	}
	if g.reviewer == nil {
		return fmt.Errorf("%w: %s requires interactive approval", ErrDenied, req.Name)
	}

	decision, err := g.reviewer.Review(ctx, req)
	if err != nil {
		return fmt.Errorf("%w: approval failed: %v", ErrDenied, err)
	}
	if !decision.Allow {
		return fmt.Errorf("%w: user declined %s", ErrDenied, req.Name)
	}
	if decision.AllowSession && req.Tier == Medium && g.permissions.AllowSession {
		g.mu.Lock()
		g.session[key] = struct{}{}
		g.mu.Unlock()
	}
	return nil
}

func tierFor(name string) Tier {
	switch name {
	case "read_file", "glob", "grep":
		return Low
	case "write_file", "edit_file", "bash":
		return Medium
	default:
		// Unknown and future tools must be reviewed rather than accidentally
		// inheriting a low-risk default.
		return High
	}
}

func (g *Guard) mode(req Request) string {
	switch req.Name {
	case "read_file", "glob", "grep":
		return g.permissions.Read
	case "write_file", "edit_file":
		return g.permissions.Write
	case "bash":
		return g.permissions.Shell
	default:
		return "ask"
	}
}

func (g *Guard) hardDeny(req Request) string {
	var input struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(req.Arguments, &input); err != nil {
		return "tool arguments are not valid JSON"
	}
	if input.Path != "" {
		for _, pattern := range g.permissions.WriteDeny {
			if matchesPath(pattern, input.Path) {
				return fmt.Sprintf("path %q matches write_deny pattern %q", input.Path, pattern)
			}
		}
	}
	if req.Name == "bash" {
		for _, pattern := range g.permissions.ShellDeny {
			if matches(pattern, input.Command) {
				return fmt.Sprintf("command matches shell_deny pattern %q", pattern)
			}
		}
	}
	return ""
}

func (g *Guard) hasSessionGrant(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.session[key]
	return ok
}

func requestKey(req Request) string {
	return req.Name + "\x00" + string(req.Arguments)
}

func clone(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func matchesPath(pattern, value string) bool {
	if matches(filepath.ToSlash(pattern), filepath.ToSlash(value)) {
		return true
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return matches(filepath.ToSlash(pattern), filepath.ToSlash(filepath.Join(home, value[2:])))
		}
	}
	return false
}

// matches accepts the documented ** glob form in addition to filepath.Match's
// single-path-component star. It deliberately remains small and deterministic.
func matches(pattern, value string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()).MatchString(value)
}
