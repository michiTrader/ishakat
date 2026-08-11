package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/theme"
)

func approvalRequest(tier permissions.Tier) permissions.Request {
	return permissions.Request{
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"notes.txt","content":"hello"}`),
		Tier:      tier,
	}
}

func TestNewToolApproveDialogOffersSessionGrantOnlyForMediumRisk(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tier        permissions.Tier
		wantRows    int
		wantSession bool // whether any row offers AllowSession = true
	}{
		{name: "medium", tier: permissions.Medium, wantRows: 3, wantSession: true},
		{name: "high", tier: permissions.High, wantRows: 2, wantSession: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialog := newToolApproveDialog(approvalRequest(tc.tier), make(chan permissions.Decision, 1))
			if len(dialog.options) != tc.wantRows {
				t.Fatalf("options = %d, want %d", len(dialog.options), tc.wantRows)
			}
			gotSession := false
			for _, option := range dialog.options {
				if option.decision.AllowSession {
					gotSession = true
				}
			}
			if gotSession != tc.wantSession {
				t.Errorf("dialog offers AllowSession row = %v, want %v (options: %+v)", gotSession, tc.wantSession, dialog.options)
			}
			// The first row is always "allow once" and the last is always
			// "deny" — neither must ever carry AllowSession, regardless of
			// tier; only the middle Medium-only row does.
			if first := dialog.options[0]; first.decision.AllowSession {
				t.Errorf("first option %+v must not offer AllowSession", first)
			}
			if last := dialog.options[len(dialog.options)-1]; last.decision.AllowSession || last.decision.Allow {
				t.Errorf("last option %+v must be an explicit deny", last)
			}
		})
	}
}

func TestToolApproveDialogSelectionWrapsAndCancelDenies(t *testing.T) {
	reply := make(chan permissions.Decision, 1)
	dialog := newToolApproveDialog(approvalRequest(permissions.Medium), reply)
	if got := dialog.moveSel(-1).sel; got != len(dialog.options)-1 {
		t.Fatalf("selection after moving up from first row = %d, want last row", got)
	}
	if got := dialog.moveSel(1).moveSel(1).moveSel(1).sel; got != 0 {
		t.Fatalf("selection after wrapping through all rows = %d, want 0", got)
	}

	root := Root{mode: ModeToolApprove, keys: Map{Cancel: "esc", Submit: "enter"}, toolApprove: dialog}
	model, _ := root.updateToolApprove(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := model.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode after cancel = %v, want ModeBusy", got.mode)
	}
	if got.toolApprove.reply != nil || len(got.toolApprove.options) != 0 {
		t.Fatalf("approval state was not cleared: %+v", got.toolApprove)
	}
	select {
	case decision := <-reply:
		if decision.Allow || decision.AllowSession {
			t.Fatalf("cancel decision = %+v, want explicit deny", decision)
		}
	default:
		t.Fatal("cancel did not answer the reviewer channel")
	}
}

func TestToolApproveDialogSubmitSendsSelectedDecision(t *testing.T) {
	reply := make(chan permissions.Decision, 1)
	dialog := newToolApproveDialog(approvalRequest(permissions.Medium), reply).moveSel(1)
	root := Root{mode: ModeToolApprove, keys: Map{Cancel: "esc", Submit: "enter"}, toolApprove: dialog}

	model, _ := root.updateToolApprove(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := model.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode after submit = %v, want ModeBusy", got.mode)
	}
	select {
	case decision := <-reply:
		if !decision.Allow || !decision.AllowSession {
			t.Fatalf("submitted decision = %+v, want medium session approval", decision)
		}
	default:
		t.Fatal("submit did not answer the reviewer channel")
	}
}

// TestRenderManifestProvenanceGenericToolFallsThrough is the negative case
// every tool other than tool_create/tool_edit must take: no structured
// view exists for write_file, so renderManifestProvenance must report
// ok=false and leave renderToolApprove's own fallback to wrapArgsLines
// untouched -- this is what keeps read_file/bash/etc. showing exactly the
// generic JSON dump they showed before this file grew a special case.
func TestRenderManifestProvenanceGenericToolFallsThrough(t *testing.T) {
	_, ok := renderManifestProvenance("write_file", approvalRequest(permissions.Medium).Arguments, 60)
	if ok {
		t.Fatal("write_file must not get the structured manifest view")
	}
}

// TestRenderManifestProvenanceToolCreateSurfacesProvenance is the positive
// case §19.6 gate 2 exists for: a tool_create request's reason/sources/
// origin/repetitions must all be readable as their own labeled lines, not
// buried in one undifferentiated JSON object -- this is the exact gap
// more than one §17 Bitácora entry named ("richer manifest+provenance
// dialog view... not a self-extension-aware one").
func TestRenderManifestProvenanceToolCreateSurfacesProvenance(t *testing.T) {
	args := json.RawMessage(`{
		"name": "bybit_price",
		"description": "consulta el precio spot de Bybit",
		"method": "GET",
		"url": "https://api.bybit.com/v5/market/tickers",
		"origin": "agent",
		"reason": "detected_repetition",
		"sources": ["https://bybit-exchange.github.io/docs/v5/market/tickers"],
		"session_id": "sess-42",
		"repetitions": 5
	}`)
	lines, ok := renderManifestProvenance("tool_create", args, 80)
	if !ok {
		t.Fatal("tool_create must get the structured manifest view")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"bybit_price",
		"consulta el precio spot de Bybit",
		"GET https://api.bybit.com/v5/market/tickers",
		"el agente (detectó repetición)",
		"detected_repetition",
		"repeticiones observadas: 5",
		"bybit-exchange.github.io",
		"sess-42",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered manifest missing %q; got:\n%s", want, joined)
		}
	}
}

// TestRenderManifestProvenanceToolCreateNoSourcesIsExplicit checks the
// "mandatory provenance" half of §19.8 mitigation 2 on the display side:
// an empty (but present) sources list must render as an explicit "none
// declared" line, not silently vanish the way omitempty would make a
// generic JSON dump do -- the whole point of making Sources non-omitempty
// in tool_create.go's own argument struct is that a human approving the
// tool can tell "declared no sources" apart from "did not even address
// it", and the dialog must preserve that distinction, not erase it.
func TestRenderManifestProvenanceToolCreateNoSourcesIsExplicit(t *testing.T) {
	args := json.RawMessage(`{
		"name": "local_only",
		"method": "GET",
		"url": "https://example.com/x",
		"origin": "user_forced",
		"reason": "forced",
		"sources": []
	}`)
	lines, ok := renderManifestProvenance("tool_create", args, 80)
	if !ok {
		t.Fatal("tool_create must get the structured manifest view")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "fuentes: (ninguna declarada)") {
		t.Errorf("empty sources must render as an explicit no-sources line; got:\n%s", joined)
	}
	if !strings.Contains(joined, "vos (forzado)") {
		t.Errorf("origin user_forced must render its Spanish label; got:\n%s", joined)
	}
}

// TestRenderManifestProvenanceToolEditShowsBeforeAfter is tool_edit's
// analogue of the tool_create test above: the human approving a patch to
// an already-installed tool needs to see the exact old_string/new_string
// pair, not a JSON object with those two keys.
func TestRenderManifestProvenanceToolEditShowsBeforeAfter(t *testing.T) {
	args := json.RawMessage(`{
		"name": "bybit_price",
		"old_string": "url = \"https://api.bybit.com/v5/market/tickers\"",
		"new_string": "url = \"https://api.bybit.com/v5/market/tickers-v2\"",
		"replace_all": true
	}`)
	lines, ok := renderManifestProvenance("tool_edit", args, 80)
	if !ok {
		t.Fatal("tool_edit must get the structured manifest view")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"bybit_price",
		"tickers\"",
		"tickers-v2\"",
		"todas las ocurrencias",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered edit view missing %q; got:\n%s", want, joined)
		}
	}
}

// TestRenderToolApproveUsesStructuredViewForToolCreate is the integration
// check that renderToolApprove itself -- not just the helper in
// isolation -- actually takes the structured branch for a tool_create
// request end to end, through the same Root.renderToolApprove a live
// ModeToolApprove overlay calls.
func TestRenderToolApproveUsesStructuredViewForToolCreate(t *testing.T) {
	req := permissions.Request{
		Name: "tool_create",
		Arguments: json.RawMessage(`{
			"name": "bybit_price",
			"method": "GET",
			"url": "https://api.bybit.com/v5/market/tickers",
			"origin": "user_declared",
			"reason": "declared_recurring_workflow",
			"sources": []
		}`),
		Tier: permissions.High,
	}
	root := Root{
		mode:        ModeToolApprove,
		lay:         Layout{Width: 80, Glyphs: theme.GlyphsUnicode},
		styles:      theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode),
		keys:        Map{Cancel: "esc", Submit: "enter"},
		toolApprove: newToolApproveDialog(req, make(chan permissions.Decision, 1)),
	}
	out := root.renderToolApprove()
	if !strings.Contains(out, "vos (flujo declarado)") {
		t.Errorf("renderToolApprove did not use the structured tool_create view; got:\n%s", out)
	}
	if strings.Contains(out, `"origin"`) {
		t.Errorf("renderToolApprove fell back to the generic JSON dump for tool_create; got:\n%s", out)
	}
}
