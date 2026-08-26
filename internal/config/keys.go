package config

import (
	"fmt"
	"strings"
	"unicode"
)

// validateKeys checks the [keys] table (RC-1).
//
// Every binding has to be a single chord this build can actually produce:
// tea.KeyPressMsg.String() is one keystroke (uv.Key.String / Keystroke), so a
// value like "ctrl+c ctrl+c" can never match. A typo that silently disabled
// quit is how RC-1 shipped; refusing to start is the only honest option.
//
// Empty values are allowed — NewMap fills them from defaultMap. The one
// last-resort exception is a multi-word quit whose tokens are all the same
// chord: that used to be how "press twice" was written, so we keep the chord
// once and, if QuitRepeat is unset, set it to the token count.
func validateKeys(c *Config) error {
	k := &c.Keys

	if err := normalizeQuit(k, c); err != nil {
		return err
	}
	if k.QuitRepeat < 0 {
		return fmt.Errorf("[keys] quit_repeat = %d cannot be negative", k.QuitRepeat)
	}
	if k.QuitRepeat > maxQuitRepeat {
		return fmt.Errorf("[keys] quit_repeat = %d is too large (maximum %d)", k.QuitRepeat, maxQuitRepeat)
	}
	if k.QuitRepeat == 0 {
		k.QuitRepeat = defaultQuitRepeat
	}

	for _, f := range []struct {
		name  string
		value string
	}{
		{"submit", k.Submit},
		{"newline", k.Newline},
		{"cancel", k.Cancel},
		{"quit", k.Quit},
		{"clear_screen", k.ClearScreen},
		{"model_picker", k.ModelPicker},
		{"model_cycle", k.ModelCycle},
		{"theme_picker", k.ThemePicker},
		{"history_prev", k.HistoryPrev},
		{"history_next", k.HistoryNext},
		{"copy_last", k.CopyLast},
		{"toggle_fold", k.ToggleFold},
		{"queue_followup", k.QueueFollowup},
		{"edit_queue", k.EditQueue},
		{"effort_cycle", k.EffortCycle},
		{"scroll_up", k.ScrollUp},
		{"scroll_down", k.ScrollDown},
	} {
		if f.value == "" {
			continue
		}
		if !validChord(f.value) {
			return fmt.Errorf("[keys] %s = %q is not a chord this build can produce (tea.KeyPressMsg.String() is one keystroke; use e.g. \"ctrl+c\", \"enter\", \"esc\")",
				f.name, f.value)
		}
	}
	return nil
}

// defaultQuitRepeat is the shipped "press twice to quit" semantic (§7.4).
const defaultQuitRepeat = 2

// maxQuitRepeat is a sanity cap: a value large enough that quit is
// effectively unreachable is the same class of bug as a chord that never
// matches.
const maxQuitRepeat = 10

// normalizeQuit rewrites the legacy multi-word form quit = "ctrl+c ctrl+c"
// into a single chord plus a repeat count. Tokens must all be the same
// chord; a mixed sequence has no meaning and is a hard error.
func normalizeQuit(k *Keys, c *Config) error {
	parts := strings.Fields(k.Quit)
	if len(parts) < 2 {
		return nil
	}
	first := parts[0]
	for _, p := range parts[1:] {
		if p != first {
			return fmt.Errorf("[keys] quit = %q mixes different chords; write a single chord and set quit_repeat", k.Quit)
		}
	}
	if !validChord(first) {
		return fmt.Errorf("[keys] quit = %q is not a chord this build can produce", first)
	}
	if k.QuitRepeat == 0 {
		k.QuitRepeat = len(parts)
	} else if k.QuitRepeat != len(parts) {
		c.Warnings = append(c.Warnings, Warning{"keys",
			fmt.Sprintf("quit listed %d times but quit_repeat = %d; keeping quit_repeat", len(parts), k.QuitRepeat)})
	}
	c.Warnings = append(c.Warnings, Warning{"keys",
		fmt.Sprintf("quit = %q rewritten as quit = %q and quit_repeat = %d (RC-1: tea.KeyPressMsg.String() is one keystroke)",
			k.Quit, first, k.QuitRepeat)})
	k.Quit = first
	return nil
}

// validChord reports whether s is a single keystroke this build's
// tea.KeyPressMsg.String() / uv.Key.Keystroke can produce: optional modifiers
// in Keystroke order (ctrl, alt, shift, meta, hyper, super) plus a named key
// (enter, esc, up, f1…f24, …) or a single printable rune.
func validChord(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	parts := strings.Split(strings.ToLower(s), "+")
	if len(parts) == 0 {
		return false
	}
	key := parts[len(parts)-1]
	seen := map[string]bool{}
	for _, mod := range parts[:len(parts)-1] {
		if _, ok := chordModifiers[mod]; !ok || seen[mod] || mod == "" {
			return false
		}
		seen[mod] = true
	}
	if key == "" {
		return false
	}
	if _, ok := namedKeys[key]; ok {
		return true
	}
	runes := []rune(key)
	if len(runes) != 1 {
		return false
	}
	r := runes[0]
	return unicode.IsPrint(r) && r != '+'
}

// chordModifiers is uv.Key.Keystroke's modifier vocabulary, in the order
// Keystroke itself prints them.
var chordModifiers = map[string]struct{}{
	"ctrl": {}, "alt": {}, "shift": {}, "meta": {}, "hyper": {}, "super": {},
}

// namedKeys is the subset of ultraviolet's keyTypeString / stringKeyType a
// user can reasonably put in [keys]. f1–f24 covers every function key a
// real keyboard has; the rest are the named keys Keystroke emits.
var namedKeys = map[string]struct{}{
	"enter": {}, "tab": {}, "backspace": {}, "esc": {}, "escape": {},
	"space": {}, "up": {}, "down": {}, "left": {}, "right": {},
	"begin": {}, "find": {}, "insert": {}, "delete": {}, "select": {},
	"pgup": {}, "pgdown": {}, "home": {}, "end": {},
	"f1": {}, "f2": {}, "f3": {}, "f4": {}, "f5": {}, "f6": {},
	"f7": {}, "f8": {}, "f9": {}, "f10": {}, "f11": {}, "f12": {},
	"f13": {}, "f14": {}, "f15": {}, "f16": {}, "f17": {}, "f18": {},
	"f19": {}, "f20": {}, "f21": {}, "f22": {}, "f23": {}, "f24": {},
}
