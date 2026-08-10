package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// bodyRecorder is an httptest server that keeps every request body it was
// sent. It exists because the Step 16 bug this file guards against was
// invisible from every other vantage point: the reviewer, the dialog, the
// guard and the agent loop each had their own passing unit test against
// their own fake, and the program still could not call a single tool —
// because nothing ever looked at the bytes on the wire, which is where the
// missing `tools` array actually was. Asserting on the request body is not a
// nicety here; it is the only layer at which this regression is detectable.
type bodyRecorder struct {
	srv *httptest.Server

	mu     sync.Mutex
	bodies []map[string]any
}

func newBodyRecorder(t *testing.T) *bodyRecorder {
	t.Helper()
	rec := &bodyRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			rec.mu.Lock()
			rec.bodies = append(rec.bodies, parsed)
			rec.mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fake.SSEDelta("ok")))
		_, _ = w.Write([]byte(fake.SSEDone()))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// lastBody is the most recent decoded request body, failing the test when no
// request ever arrived — "the assertion passed because nothing was sent" is
// exactly the shape of false confidence this file exists to prevent.
func (r *bodyRecorder) lastBody(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("the provider received no request at all")
	}
	return r.bodies[len(r.bodies)-1]
}

// toolNamesIn extracts the function names from a decoded body's `tools`
// array, or nil when the field is absent — the state the bug produced, and
// therefore the state this helper has to be able to report.
func toolNamesIn(body map[string]any) []string {
	arr, ok := body["tools"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, entry := range arr {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := obj["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// runOneTurn drives a single turn through eng so the streamer actually
// performs its HTTP request, which is what puts a body in the recorder. The
// request carries the real seven-tool catalogue: what is being tested is
// whether the Caps gate lets them through to the wire, so the tools have to
// be genuinely present in the engine.Request for their absence downstream to
// mean anything.
func runOneTurn(t *testing.T, eng *engine.Engine, wireID string) {
	t.Helper()
	var buf engine.StreamBuf
	eng.Start(t.Context(), engine.Request{
		Model:    wireID,
		Messages: []convo.Message{convo.User("create a file")},
		Tools:    ToolDefsFrom(tools.Core(nil, false)),
	}, &buf)
	drainEngineTest(t, &buf)
}

// catalogWith builds a one-model catalog for ref with the given tool
// support, so a test states the catalog's opinion in one line instead of
// constructing a realistic snapshot.
func catalogWith(ref string, tools bool) *catalog.Catalog {
	provID, wireID, _ := catalog.SplitRef(ref)
	return &catalog.Catalog{Models: []catalog.Model{{
		Ref:      ref,
		Provider: provID,
		WireID:   wireID,
		Caps:     catalog.Caps{Tools: tools},
	}}}
}

// TestBuildEngineSendsToolsWhenEnabled is the regression test for the bug
// that made the whole Step 16 tool layer inert: [tools].enabled = true and
// the request body still carried no `tools` array, so the model had nothing
// to call, the guard was never consulted, and the approval overlay had
// nothing to intercept.
func TestBuildEngineSendsToolsWhenEnabled(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL)
	cfg.Tools.Enabled = true

	eng, ref, _, _, err := BuildEngine(cfg, catalogWith("omniroute/auto/coding", true), "", "0.0.0-test", true)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	runOneTurn(t, eng, ref.WireID)

	names := toolNamesIn(rec.lastBody(t))
	if len(names) == 0 {
		t.Fatal("the request body carried no `tools` array: the model is being offered no tools, " +
			"which is exactly the bug that made [tools].enabled do nothing")
	}
	if !containsString(names, "write_file") {
		t.Errorf("tools on the wire = %v, want write_file among them", names)
	}
}

// TestBuildEngineOmitsToolsWhenDisabled is the other half of the contract:
// the default configuration must not start sending a `tools` array to every
// provider just because the plumbing now exists.
func TestBuildEngineOmitsToolsWhenDisabled(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL) // Tools.Enabled defaults to false.

	eng, ref, _, _, err := BuildEngine(cfg, catalogWith("omniroute/auto/coding", true), "", "0.0.0-test", true)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	runOneTurn(t, eng, ref.WireID)

	if names := toolNamesIn(rec.lastBody(t)); len(names) != 0 {
		t.Errorf("tools on the wire = %v, want none with [tools].enabled = false", names)
	}
}

// TestBuildEngineOmitsToolsForCompaction pins §10's rule at the wire: the
// compact_model engine is built with wantTools = false, so a summarizer can
// never be handed write_file or bash even in a tools-enabled session.
func TestBuildEngineOmitsToolsForCompaction(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL)
	cfg.Tools.Enabled = true

	eng, ref, _, _, err := BuildEngine(cfg, catalogWith("omniroute/auto/coding", true), "", "0.0.0-test", false)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	runOneTurn(t, eng, ref.WireID)

	if names := toolNamesIn(rec.lastBody(t)); len(names) != 0 {
		t.Errorf("tools on the wire = %v, want none for a compaction engine", names)
	}
}

// TestBuildEngineWarnsAndOmitsToolsForAToolIncapableModel covers the one
// case where refusing is right: the catalog positively says this model has
// no tool support, so sending the array would earn a 400 on every turn. The
// user has to be told, since the feature they switched on is not in effect.
func TestBuildEngineWarnsAndOmitsToolsForAToolIncapableModel(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL)
	cfg.Tools.Enabled = true

	eng, ref, _, warn, err := BuildEngine(cfg, catalogWith("omniroute/auto/coding", false), "", "0.0.0-test", true)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if !strings.Contains(warn, "no tool-calling support") {
		t.Errorf("warn = %q, want it to name the missing tool support", warn)
	}
	runOneTurn(t, eng, ref.WireID)

	if names := toolNamesIn(rec.lastBody(t)); len(names) != 0 {
		t.Errorf("tools on the wire = %v, want none for a model the catalog says cannot take them", names)
	}
}

// TestBuildEngineSendsToolsForAModelMissingFromTheCatalog is the deliberate
// asymmetry in CapsFor: an absent catalog row is ignorance, not evidence of
// absence. Guessing "no tools" for an unknown model is what would turn this
// feature into a silent no-op on a fresh install (no cache yet), so the
// explicit opt-in wins and a wrong guess surfaces as a loud provider error
// instead of a model that quietly cannot act.
func TestBuildEngineSendsToolsForAModelMissingFromTheCatalog(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL)
	cfg.Tools.Enabled = true

	eng, ref, _, warn, err := BuildEngine(cfg, nil, "", "0.0.0-test", true)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty: an unknown model is not a downgrade to report", warn)
	}
	runOneTurn(t, eng, ref.WireID)

	if names := toolNamesIn(rec.lastBody(t)); len(names) == 0 {
		t.Error("the request body carried no `tools` array for a model absent from the catalog, " +
			"which would make tool calling silently dead on any first run (empty cache)")
	}
}

// TestEngineFactoryReDecidesToolsPerModel is the picker's own regression:
// Caps is resolved per destination ref, not captured once at boot, so
// switching to a tool-incapable model stops sending the array and switching
// back resumes it. Binding the boot model's answer would have let ctrl+p
// break tool calling with no recovery short of a restart.
func TestEngineFactoryReDecidesToolsPerModel(t *testing.T) {
	rec := newBodyRecorder(t)
	cfg := cfgFor(t, rec.srv.URL)
	cfg.Tools.Enabled = true

	cat := &catalog.Catalog{Models: []catalog.Model{
		{Ref: "omniroute/auto/coding", Provider: "omniroute", WireID: "auto/coding", Caps: catalog.Caps{Tools: true}},
		{Ref: "omniroute/gpt-5", Provider: "omniroute", WireID: "gpt-5", Caps: catalog.Caps{Tools: false}},
	}}
	factory := NewEngineFactory(cfg, cat, "0.0.0-test", true)

	capable, err := factory("omniroute/auto/coding")
	if err != nil {
		t.Fatalf("factory(tool-capable): %v", err)
	}
	runOneTurn(t, capable, "auto/coding")
	if names := toolNamesIn(rec.lastBody(t)); len(names) == 0 {
		t.Error("switching to a tool-capable model sent no `tools` array")
	}

	incapable, err := factory("omniroute/gpt-5")
	if err != nil {
		t.Fatalf("factory(tool-incapable): %v", err)
	}
	runOneTurn(t, incapable, "gpt-5")
	if names := toolNamesIn(rec.lastBody(t)); len(names) != 0 {
		t.Errorf("switching to a tool-incapable model still sent %v", names)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
