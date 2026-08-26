package tui

// Tests for Bug 1's own follow-up report: scrolling up past the top of
// content kept accumulating Root.scrollOffset past what was ever visible,
// so scrolling back down had to first "pay back" that invisible debt
// before the view moved at all. See scrollBy's own doc comment (root.go)
// and maxScrollOffset's (view.go) for the fix.

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// newScrollTestRoot builds a Root with enough transcript entries to force
// clipHead's clip to actually engage, sized so headBudget() is small and
// predictable. Each entry is a single line (no markdown/wrapping surprises,
// ASCII glyphs for deterministic widths) so the row-count arithmetic below
// is exact.
func newScrollTestRoot(t *testing.T, width, height, numEntries int) Root {
	t.Helper()
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  theme.GlyphsASCII,
		NoTTY:   false,
	})
	m, _ := root.Update(tea.WindowSizeMsg{Width: width, Height: height})
	r := m.(Root)
	for i := 0; i < numEntries; i++ {
		r.transcript = append(r.transcript, transcriptEntry{
			role: "user", name: "tu", text: "linea de mensaje numero " + itoaScroll(i),
		})
	}
	return r
}

// itoaScroll avoids pulling in strconv just for a handful of test messages.
func itoaScroll(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// TestScrollByNeverExceedsMaxScrollOffset is the direct regression for the
// reported bug: scrolling up far past the point where clipHead's own
// window has already hit the top of content must not leave
// Root.scrollOffset sitting past maxScrollOffset() — the old code let it
// grow unbounded (only floor-clamped), which is exactly the "invisible
// debt" the user described (50 wheel-lines of scrollBy(+3) calls, only
// ~10 lines of real headroom, and the view stuck at the ceiling while the
// field itself kept climbing to 150).
func TestScrollByNeverExceedsMaxScrollOffset(t *testing.T) {
	r := newScrollTestRoot(t, 60, 14, 6) // few entries, small headBudget()

	max := r.maxScrollOffset()
	if max <= 0 {
		t.Fatalf("test setup: maxScrollOffset() = %d, want > 0 so this test can exercise the ceiling (increase numEntries or shrink the terminal)", max)
	}

	// Scroll up by far more than the real headroom — the wheel's own
	// 3-row step, repeated the same "50 ticks" order of magnitude the
	// user reported.
	for i := 0; i < 50; i++ {
		r = r.scrollBy(3)
	}

	if r.scrollOffset != max {
		t.Fatalf("after scrolling up 50 ticks past the top, scrollOffset = %d, want exactly maxScrollOffset() = %d (it must not accumulate past what is visible)", r.scrollOffset, max)
	}

	// Symmetry: once pinned at the ceiling, scrolling back down by
	// exactly the same distance the view can actually move must reach
	// the tail in that many ticks — not 50, and not "40 ticks of nothing
	// followed by 10 that finally move". If the old bug were still
	// present, r.scrollOffset would have been sitting at 150 here, and
	// undoing it would take 50 ticks to reach 0, most of them moving
	// nothing visible.
	ticksToReachZero := 0
	for r.scrollOffset > 0 {
		r = r.scrollBy(-3)
		ticksToReachZero++
		if ticksToReachZero > 50 {
			t.Fatalf("scrolling back down did not reach 0 within 50 ticks (still %d); the accumulation bug appears to still be present", r.scrollOffset)
		}
	}

	// max was reached by ceil(max/3) ticks of +3; the same number of -3
	// ticks (rounding the same way) must undo it exactly, with no extra
	// "invisible" ticks spent paying back a debt beyond what was ever
	// drawn.
	wantTicks := (max + 2) / 3
	if ticksToReachZero != wantTicks {
		t.Errorf("scrolling back down to 0 took %d ticks, want %d (maxScrollOffset()=%d) — a mismatch here means some of the up-scrolling was silently retained past the visible ceiling", ticksToReachZero, wantTicks, max)
	}
}

// TestScrollByCeilingMatchesClipHeadsOwnClamp cross-checks
// Root.maxScrollOffset (view.go) directly against what clipHead itself
// clamps an out-of-range offset down to, for the exact same content and
// budget — the two must never disagree, or the write-time clamp (scrollBy)
// and the read-time clamp (clipHead's per-frame render) could each allow a
// different ceiling and reintroduce a milder version of the same bug.
func TestScrollByCeilingMatchesClipHeadsOwnClamp(t *testing.T) {
	r := newScrollTestRoot(t, 60, 14, 6)

	max := r.maxScrollOffset()
	if max <= 0 {
		t.Fatalf("test setup: maxScrollOffset() = %d, want > 0", max)
	}

	// Ask clipHead to render at an offset far past max — it has its own
	// internal clamp (the same maxScrollOffsetFor helper) and must
	// produce the identical clipped window whether asked for max or for
	// something absurdly larger.
	g := r.lay.glyphs()
	atMax := clipHead(r.headContent(), g, r.headBudget(), max)
	wayPast := clipHead(r.headContent(), g, r.headBudget(), max+1000)

	if atMax != wayPast {
		t.Errorf("clipHead(offset=max) and clipHead(offset=max+1000) differ — its own internal clamp disagrees with Root.maxScrollOffset()'s ceiling.\nat max:\n%s\nway past:\n%s", atMax, wayPast)
	}
}

// TestScrollUpPastTopThenDownIsSymmetric is the user's own reported
// scenario end to end, through scrollWheel (the actual mouse-wheel entry
// point) rather than scrollBy directly: scroll up "a lot" (far more ticks
// than there is headroom for), confirm the visible window stopped moving
// at the top (headRows(head()) reflects the same clipped content
// throughout), then scroll back down the same number of ticks it actually
// took to reach the top and land exactly back at the tail (scrollOffset ==
// 0), not still sitting on unpaid "debt".
func TestScrollUpPastTopThenDownIsSymmetric(t *testing.T) {
	r := newScrollTestRoot(t, 60, 14, 8)

	// Scroll up one wheel tick at a time, counting exactly how many
	// ticks it takes before the offset stops changing (i.e. the top is
	// reached) — this is "however many ticks of real headroom there
	// are", which the user's own report says should also be exactly how
	// many ticks scrolling back down takes to reach the tail again.
	ticksUp := 0
	for i := 0; i < 100; i++ {
		before := r.scrollOffset
		r = r.scrollWheel(tea.MouseWheelUp)
		ticksUp++
		if r.scrollOffset == before {
			break // reached the ceiling; this tick was a no-op
		}
	}
	if ticksUp >= 100 {
		t.Fatalf("scrolling up did not stabilize within 100 ticks — maxScrollOffset() may not be clamping at all")
	}
	// One more tick past "reached the ceiling" must still be a true
	// no-op on the field itself (not merely invisible), which is the
	// crux of the whole bug report.
	stuck := r.scrollOffset
	r = r.scrollWheel(tea.MouseWheelUp)
	if r.scrollOffset != stuck {
		t.Fatalf("one more wheel-up tick at the ceiling changed scrollOffset from %d to %d; it must stay pinned at maxScrollOffset()", stuck, r.scrollOffset)
	}

	// Scrolling back down must reach 0 in exactly ticksUp-1 ticks (the
	// last "up" tick at the ceiling was a genuine no-op, per the
	// assertion above, so it does not count towards how far there was
	// ever real ground to cover).
	realTicksUp := ticksUp - 1
	for i := 0; i < realTicksUp; i++ {
		r = r.scrollWheel(tea.MouseWheelDown)
	}
	if r.scrollOffset != 0 {
		t.Fatalf("after scrolling down the same %d ticks it took to reach the top, scrollOffset = %d, want 0 (no leftover invisible debt)", realTicksUp, r.scrollOffset)
	}
}
