package tui

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
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
		name      string
		tier      permissions.Tier
		wantRows  int
		wantGrant bool
	}{
		{name: "medium", tier: permissions.Medium, wantRows: 3, wantGrant: true},
		{name: "high", tier: permissions.High, wantRows: 2, wantGrant: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialog := newToolApproveDialog(approvalRequest(tc.tier), make(chan permissions.Decision, 1))
			if len(dialog.options) != tc.wantRows {
				t.Fatalf("options = %d, want %d", len(dialog.options), tc.wantRows)
			}
			for _, option := range dialog.options {
				if option.decision.AllowSession != tc.wantGrant {
					t.Errorf("option %+v has AllowSession = %v, want %v", option, option.decision.AllowSession, tc.wantGrant)
				}
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
	root := Root{mode: ModeToolApprove, toolApprove: dialog}

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
