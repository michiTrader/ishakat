// session_mission_test.go closes §21.16 decision 3's own remaining test
// gap: sessionRecorder.AppendMission (session.go) and replayMissions
// (app.go) are each covered by convo's own round-trip test and by
// internal/tui's own wiring regression test, but neither of *those* proves
// the two halves this package owns — persisting a mission event to the
// real on-disk session file, and replaying restored events back onto a
// real *permissions.Guard before any tool call — actually do what §21.16
// decision 3 requires. This file is that proof, at the internal/app layer
// where both halves actually meet.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/permissions"
)

// TestSessionRecorderAppendMissionCreatesFileOnFirstCall mirrors
// TestSessionRecorderCreatesFileOnlyOnFirstAppend, but for the mission
// event kind: a mission can resolve before the user's own message is ever
// recorded (resolveToolScope calls recordMission before submit's own
// recordMessage call for the same turn — see AppendMission's own doc
// comment), so AppendMission itself has to be able to lazily create the
// session file, not only Append.
func TestSessionRecorderAppendMissionCreatesFileOnFirstCall(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	r, warn := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	rec, ok := r.(*sessionRecorder)
	if !ok {
		t.Fatalf("NewSessionRecorder returned %T, want *sessionRecorder", r)
	}

	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Fatalf("a file was created before the first AppendMission: %v", files)
	}

	ev := convo.MissionEvent{
		Goal:      "fix orbital-dash, no playwright",
		Rules:     []convo.MissionRule{{Capability: "bash", Pattern: "playwright"}},
		BashScope: []string{"npm", "git"},
	}
	if err := rec.AppendMission(ev); err != nil {
		t.Fatalf("AppendMission: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files after the first AppendMission = %d, want 1", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (header, mission):\n%s", len(lines), raw)
	}
	if !strings.Contains(lines[0], `"type":"header"`) {
		t.Errorf("first line must be the header: %s", lines[0])
	}
	// Same titleFrom rule Append itself uses — AppendMission's own lazy
	// creation must agree on a title with what Append would have given
	// the same turn a few lines later, not win arbitrarily.
	if !strings.Contains(lines[0], `"title":"fix orbital-dash, no playwright"`) {
		t.Errorf("unexpected title: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"type":"mission"`) {
		t.Errorf("second line must be the mission event: %s", lines[1])
	}

	// Once a mission event has lazily created the file, the turn's own
	// user message (Append, not AppendMission) must land in the *same*
	// file, not a second one — this is the ordering resolveToolScope's
	// own recordMission-before-submit call actually produces in a real
	// session.
	if err := rec.Append(convo.User(ev.Goal)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files after Append = %d, want still 1 (no second conversation created)", len(files))
	}
	raw, _ = os.ReadFile(files[0])
	if strings.Count(string(raw), `"type":"header"`) != 1 {
		t.Errorf("expected exactly one header line, got:\n%s", raw)
	}
}

// TestSessionRecorderAppendMissionAppendsToAResumedConversation is
// AppendMission's own mirror of TestSessionRecorderAppendsToAResumedConversation:
// a mission resolved mid-way through a resumed session appends to that same
// file rather than starting a new one.
func TestSessionRecorderAppendMissionAppendsToAResumedConversation(t *testing.T) {
	dir := t.TempDir()
	store, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.New("charla previa", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(seeded.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}

	rec := &sessionRecorder{store: store, conv: seeded, model: "openai/gpt-4o"}
	ev := convo.MissionEvent{Goal: "no network", Rules: []convo.MissionRule{{Capability: "fetch", Pattern: "*"}}}
	if err := rec.AppendMission(ev); err != nil {
		t.Fatalf("AppendMission: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files = %d, want still 1 (no new conversation created)", len(files))
	}

	got, err := store.Load(seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Missions) != 1 || got.Missions[0].Goal != ev.Goal {
		t.Fatalf("mission event did not land in the resumed conversation: %+v", got.Missions)
	}
}

// TestReplayMissionsEnforcesRestoredRulesAndScope is §21.16 decision 3's
// own second consequence, proven directly against a real
// *permissions.Guard: every restored event's Rules end up on the Guard
// (accumulated across events, mirroring AddMissionRules' own "appends,
// never replaces" contract), and BashScope ends up set to only the last
// event's value (mirroring SetBashScope's own "replaces, never
// accumulates" contract) — not merged, not left at the first event's
// choice.
func TestReplayMissionsEnforcesRestoredRulesAndScope(t *testing.T) {
	guard := permissions.New(config.Permissions{}, false, nil)

	events := []convo.MissionEvent{
		{
			Goal:      "fix orbital-dash, no playwright",
			Rules:     []convo.MissionRule{{Capability: "bash", Pattern: "playwright"}},
			BashScope: []string{"npm", "git"},
		},
		{
			Goal:      "now also touch payments, no network",
			Rules:     []convo.MissionRule{{Capability: "fetch", Pattern: "*"}},
			BashScope: []string{"npm", "git", "go"},
		},
	}

	replayMissions(guard, events)

	rules := guard.MissionRules()
	if len(rules) != 2 {
		t.Fatalf("MissionRules() after replay = %d, want 2 (accumulated across both events): %+v", len(rules), rules)
	}
	want := map[permissions.MissionRule]bool{
		{Capability: "bash", Pattern: "playwright"}: true,
		{Capability: "fetch", Pattern: "*"}:         true,
	}
	for _, r := range rules {
		if !want[r] {
			t.Errorf("unexpected restored rule: %+v", r)
		}
	}

	scope := guard.BashScope()
	if strings.Join(scope, ",") != "npm,git,go" {
		t.Errorf("BashScope() after replay = %v, want the *last* event's scope (npm,git,go), not the first's or a merge", scope)
	}
}

// TestReplayMissionsNilGuardIsANoOp mirrors missionGuardOrNil's own "tools
// disabled" degradation: a fresh install with cfg.Tools.Enabled = false has
// no *permissions.Guard at all, and a resumed session in that state must
// not panic trying to replay onto one.
func TestReplayMissionsNilGuardIsANoOp(t *testing.T) {
	events := []convo.MissionEvent{{Goal: "no playwright", Rules: []convo.MissionRule{{Capability: "bash", Pattern: "playwright"}}}}
	replayMissions(nil, events) // must not panic
}

// TestReplayMissionsEmptyEventsIsANoOp is the ordinary case: a fresh
// session, or a resumed one that never recorded a mission at all, must
// leave the Guard exactly as permissions.New already left it.
func TestReplayMissionsEmptyEventsIsANoOp(t *testing.T) {
	guard := permissions.New(config.Permissions{}, false, nil)
	replayMissions(guard, nil)
	if len(guard.MissionRules()) != 0 {
		t.Errorf("MissionRules() after replaying nothing = %+v, want empty", guard.MissionRules())
	}
	if guard.BashScope() != nil {
		t.Errorf("BashScope() after replaying nothing = %v, want nil (never called)", guard.BashScope())
	}
}

// TestReplayMissionsLastEventEverythingInstalledClearsScope proves the
// "Everything installed" case (a nil/empty BashScope on the *last* event)
// actually clears any narrower scope an earlier event set — the same
// "invariants still apply, but the bash-subcommand restriction itself
// lifts" behaviour a live SetBashScope(nil) call already gives within one
// session, reproduced across a resume.
func TestReplayMissionsLastEventEverythingInstalledClearsScope(t *testing.T) {
	guard := permissions.New(config.Permissions{}, false, nil)
	guard.SetBashScope([]string{"npm"}) // pretend an earlier, unrelated call narrowed it

	events := []convo.MissionEvent{
		{Goal: "first", BashScope: []string{"npm", "git"}},
		{Goal: "second, everything installed", BashScope: nil},
	}
	replayMissions(guard, events)

	if scope := guard.BashScope(); scope != nil {
		t.Errorf("BashScope() after replay = %v, want nil (the last event chose Everything installed)", scope)
	}
}
