package convo_test

import (
	"os"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// TestMissionRoundTrip is §21.16 decision 3's own closing criterion: a
// MissionEvent appended via Store.AppendMission survives Load exactly, and
// interleaves correctly with ordinary messages on the same JSONL rather
// than living in a sidecar file.
func TestMissionRoundTrip(t *testing.T) {
	st, err := convo.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	c, err := st.New("misión de prueba", "omniroute/auto/coding")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := st.Append(c.ID, convo.User("fix orbital-dash, no playwright")); err != nil {
		t.Fatalf("Append user: %v", err)
	}

	ev1 := convo.MissionEvent{
		Goal:      "fix orbital-dash, no playwright",
		Rules:     []convo.MissionRule{{Capability: "bash", Pattern: "playwright"}},
		BashScope: []string{"npm", "git"},
	}
	if err := st.AppendMission(c.ID, ev1); err != nil {
		t.Fatalf("AppendMission ev1: %v", err)
	}

	if err := st.Append(c.ID, convo.Assistant("listo", "omniroute/auto/coding")); err != nil {
		t.Fatalf("Append assistant: %v", err)
	}

	// A second mission event later in the same session, to prove Missions
	// accumulates in order rather than only ever keeping the last one —
	// the same "replay every event" requirement replayMissions (internal/app)
	// depends on.
	ev2 := convo.MissionEvent{
		Goal:      "now also touch the payments module, no network",
		Rules:     []convo.MissionRule{{Capability: "fetch", Pattern: "*"}},
		BashScope: []string{"npm", "git", "go"},
	}
	if err := st.AppendMission(c.ID, ev2); err != nil {
		t.Fatalf("AppendMission ev2: %v", err)
	}

	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("esperados 2 mensajes, obtenidos %d", len(got.Messages))
	}
	if len(got.Missions) != 2 {
		t.Fatalf("esperados 2 eventos de misión, obtenidos %d", len(got.Missions))
	}

	g1, g2 := got.Missions[0], got.Missions[1]
	if g1.Goal != ev1.Goal || g2.Goal != ev2.Goal {
		t.Errorf("orden o goal de los eventos no sobrevivió: got %+v, %+v", g1, g2)
	}
	if len(g1.Rules) != 1 || g1.Rules[0] != ev1.Rules[0] {
		t.Errorf("Rules del primer evento no sobrevivió: %+v", g1.Rules)
	}
	if len(g2.Rules) != 1 || g2.Rules[0] != ev2.Rules[0] {
		t.Errorf("Rules del segundo evento no sobrevivió: %+v", g2.Rules)
	}
	if strings.Join(g1.BashScope, ",") != "npm,git" {
		t.Errorf("BashScope del primer evento no sobrevivió: %+v", g1.BashScope)
	}
	if strings.Join(g2.BashScope, ",") != "npm,git,go" {
		t.Errorf("BashScope del segundo evento no sobrevivió: %+v", g2.BashScope)
	}
	if g1.Ts.IsZero() || g2.Ts.IsZero() {
		t.Error("Ts no debería quedar en cero tras el viaje")
	}

	// The raw file itself has to carry "mission" lines, not a sidecar file
	// — §21.16 decision 3's own explicit requirement.
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if n := strings.Count(string(raw), `"type":"mission"`); n != 2 {
		t.Errorf(`esperadas 2 líneas "type":"mission" en el propio JSONL, encontradas %d`, n)
	}
}

// TestMissionSurvivesRewrite proves a MissionEvent is not silently dropped
// by rewrite() (SetTitle/Save's own non-append path) — the same guarantee
// TestSetTitleYRotate already gives ordinary messages, extended to the new
// event kind.
func TestMissionSurvivesRewrite(t *testing.T) {
	st, err := convo.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	c, err := st.New("sin título todavía", "omniroute/auto/coding")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ev := convo.MissionEvent{
		Goal:      "no network for this one",
		Rules:     []convo.MissionRule{{Capability: "fetch", Pattern: "*"}},
		BashScope: nil,
	}
	if err := st.AppendMission(c.ID, ev); err != nil {
		t.Fatalf("AppendMission: %v", err)
	}

	if err := st.SetTitle(c.ID, "orbital-dash sin red"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}

	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Title != "orbital-dash sin red" {
		t.Errorf("SetTitle no aplicó: %q", got.Title)
	}
	if len(got.Missions) != 1 || got.Missions[0].Goal != ev.Goal {
		t.Fatalf("rewrite() perdió el MissionEvent: %+v", got.Missions)
	}
}

// TestMissionEventWithNoScopeAndNoRulesStillRoundTrips is the degenerate
// case a real interaction never actually produces (resolveToolScope always
// computes a BashScope, even if nil for "Everything installed") but that
// convo itself must not choke on: an empty MissionEvent still round-trips
// as a zero-value one, distinguishable from "no event recorded at all"
// (len(Missions) == 0) by simply being present in the slice.
func TestMissionEventWithNoScopeAndNoRulesStillRoundTrips(t *testing.T) {
	st, err := convo.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	c, err := st.New("vacío", "omniroute/auto/coding")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := st.AppendMission(c.ID, convo.MissionEvent{Goal: "everything installed"}); err != nil {
		t.Fatalf("AppendMission: %v", err)
	}
	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Missions) != 1 {
		t.Fatalf("esperado 1 evento, obtenidos %d", len(got.Missions))
	}
	if len(got.Missions[0].Rules) != 0 || len(got.Missions[0].BashScope) != 0 {
		t.Errorf("evento vacío no debería traer reglas o scope: %+v", got.Missions[0])
	}
}
