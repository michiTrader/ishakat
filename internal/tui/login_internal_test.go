package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
)

// TestStartLoginWithNoFactoryReportsUnavailable covers Root.loginFor == nil
// (newHeadlessRoot never sets it, and neither does any caller with nothing
// wired): /login must report the CLI fallback instead of opening ModeLogin
// with no way to ever finish, the same nil-factory discipline switchEngine
// already follows for engineFor.
func TestStartLoginWithNoFactoryReportsUnavailable(t *testing.T) {
	root := newHeadlessRoot()
	m, cmd := root.startLogin("openai")
	if cmd != nil {
		t.Errorf("no factory wired: want a nil cmd, got %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: nothing to wait on with no factory", got.mode)
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "ishakat login") {
		t.Fatalf("expected a notice pointing at the CLI command, got %v", got.transcript)
	}
}

// fakeLoginFactory lets tests drive startLogin/finishLoginCode/finishLogin
// without a real network call, the same trackingFactory/failingFactory
// pattern engineswitch_internal_test.go already uses for EngineFactory.
type fakeLoginFactory struct {
	code    LoginDeviceCode
	waiter  LoginWaiter
	err     error
	calledW string
}

func (f *fakeLoginFactory) factory(_ context.Context, providerID string) (LoginDeviceCode, LoginWaiter, error) {
	f.calledW = providerID
	return f.code, f.waiter, f.err
}

// fakeWaiter is a LoginWaiter whose Wait returns a fixed result, for
// finishLoginCode's success path.
type fakeWaiter struct {
	note string
	err  error
}

func (w fakeWaiter) Wait(context.Context) (string, error) { return w.note, w.err }

// TestStartLoginUnknownProviderReportsError covers a provider name
// ResolveProviderPreset does not recognise: startLogin must report that
// without ever calling the factory (there is nothing to authenticate).
func TestStartLoginUnknownProviderReportsError(t *testing.T) {
	root := newHeadlessRoot()
	f := &fakeLoginFactory{}
	root.loginFor = f.factory

	m, cmd := root.startLogin("not-a-real-provider")
	if cmd != nil {
		t.Errorf("unknown provider: want a nil cmd, got %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if f.calledW != "" {
		t.Fatalf("factory should never be called for an unresolved provider, got providerID=%q", f.calledW)
	}
}

// TestStartLoginNoArgReportsUsage covers the bare "/login" case (no
// provider named): same shape as cmdLogin's own "len(rest) == 0" branch.
func TestStartLoginNoArgReportsUsage(t *testing.T) {
	root := newHeadlessRoot()
	f := &fakeLoginFactory{}
	root.loginFor = f.factory

	m, _ := root.startLogin("")
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "uso:") {
		t.Fatalf("expected a usage notice, got %v", got.transcript)
	}
}

// TestStartLoginOpensModeLoginAndFinishesOnSuccess drives the whole wizard
// end to end through fakes: startLogin opens ModeLogin and calls the
// factory, finishLoginCode moves to the "waiting" screen, and finishLogin
// (loginDoneMsg's handler) closes it with the success note.
func TestStartLoginOpensModeLoginAndFinishesOnSuccess(t *testing.T) {
	root := newHeadlessRoot()
	f := &fakeLoginFactory{
		code:   LoginDeviceCode{UserCode: "ABCD-1234", VerificationURI: "https://example.com/device"},
		waiter: fakeWaiter{note: "Configured OpenAI (openai) via OAuth device flow."},
	}
	root.loginFor = f.factory

	m, cmd := root.startLogin("openai")
	if cmd == nil {
		t.Fatal("expected a non-nil cmd to request the device code")
	}
	root = m.(Root)
	if root.mode != ModeLogin {
		t.Fatalf("mode = %v, want ModeLogin", root.mode)
	}

	// The factory itself only runs once cmd is invoked — Bubble Tea's own
	// deferred-execution contract for tea.Cmd — not synchronously inside
	// startLogin, so calledW is only meaningful after this call.
	msg := cmd()
	if f.calledW != "openai" {
		t.Fatalf("factory called with providerID=%q, want %q", f.calledW, "openai")
	}
	codeMsg, ok := msg.(loginCodeMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want loginCodeMsg", msg)
	}

	var tm tea.Model = root
	tm, cmd2 := tm.Update(codeMsg)
	root = tm.(Root)
	if !root.login.waiting {
		t.Fatal("expected login.waiting = true once the device code arrived")
	}
	if root.login.code.UserCode != "ABCD-1234" {
		t.Fatalf("login.code.UserCode = %q, want %q", root.login.code.UserCode, "ABCD-1234")
	}
	if cmd2 == nil {
		t.Fatal("expected a non-nil cmd to start waiting for the token")
	}

	msg2 := cmd2()
	doneMsg, ok := msg2.(loginDoneMsg)
	if !ok {
		t.Fatalf("cmd2 produced %T, want loginDoneMsg", msg2)
	}

	tm, _ = tm.Update(doneMsg)
	root = tm.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after a successful login", root.mode)
	}
	if len(root.transcript) != 1 || !strings.Contains(root.transcript[0].text, "Configured OpenAI") {
		t.Fatalf("expected the success note in the transcript, got %v", root.transcript)
	}
}

// TestFinishLoginSuccessChainsCatalogRefresh is F2's own hot-apply fix
// (docs/ROADMAP-ux-2026-08-20.md's W4, catalogrefresh.go): a successful
// /login must also kick off a catalog refresh, via the cmd finishLogin now
// returns, so a freshly-authenticated provider does not stay invisible
// until a separate --refresh/restart.
func TestFinishLoginSuccessChainsCatalogRefresh(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeLogin
	called := false
	wantCat := &catalog.Catalog{}
	wantCfg := &config.Config{}
	root.catalogRefreshFor = func(context.Context) (*catalog.Catalog, *config.Config) {
		called = true
		return wantCat, wantCfg
	}

	var tm tea.Model = root
	tm, cmd := tm.Update(loginDoneMsg{note: "Configured OpenAI (openai) via OAuth device flow."})
	root = tm.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", root.mode)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil cmd chaining the catalog refresh")
	}

	msg := cmd()
	if !called {
		t.Fatal("expected catalogRefreshFor to be called by the returned cmd")
	}
	// cancelLoginWith's own cmd is nil for a plain success note
	// (slashNotice with no follow-up command), so finishLogin returns
	// refreshCatalogCmd directly here rather than a tea.Batch — but the
	// logic must also tolerate a tea.BatchMsg, in case cancelLoginWith's
	// own cmd is ever non-nil in the future.
	var got CatalogRefreshedMsg
	found := false
	switch v := msg.(type) {
	case CatalogRefreshedMsg:
		got, found = v, true
	case tea.BatchMsg:
		for _, sub := range v {
			if sub == nil {
				continue
			}
			if crm, ok := sub().(CatalogRefreshedMsg); ok {
				got, found = crm, true
			}
		}
	}
	if !found {
		t.Fatalf("expected a CatalogRefreshedMsg (directly or batched), got %T", msg)
	}
	if got.Catalog != wantCat || got.Cfg != wantCfg {
		t.Fatalf("CatalogRefreshedMsg = %+v, want Catalog=%p Cfg=%p", got, wantCat, wantCfg)
	}
}

// TestFinishLoginSuccessNoCatalogRefreshFactoryFallsBack covers a nil
// catalogRefreshFor (every other test in this package, and any caller with
// nothing wired): finishLogin must fall back to its pre-F2 behaviour
// unchanged, never panicking on the unwired dependency.
func TestFinishLoginSuccessNoCatalogRefreshFactoryFallsBack(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeLogin
	var tm tea.Model = root
	tm, cmd := tm.Update(loginDoneMsg{note: "Configured OpenAI (openai) via OAuth device flow."})
	root = tm.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", root.mode)
	}
	if cmd != nil {
		if _, isBatch := cmd().(tea.BatchMsg); isBatch {
			t.Fatal("no catalogRefreshFor wired: did not expect a batched refresh cmd")
		}
	}
}

// TestFinishLoginCodeErrorClosesWizard covers RequestDeviceCode itself
// failing (e.g. an unreachable device-code endpoint): the wizard must
// close and report the error, never leaving ModeLogin open with nothing
// left to wait on.
func TestFinishLoginCodeErrorClosesWizard(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeLogin
	root.login = loginState{}

	m, cmd := root.finishLoginCode(LoginDeviceCode{}, nil, errors.New("boom: unreachable"))
	if cmd != nil {
		t.Errorf("an error has nothing left to wait on: want a nil cmd, got %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "boom: unreachable") {
		t.Fatalf("expected the error in the transcript, got %v", got.transcript)
	}
}

// TestCancelLoginClosesWizard covers esc/ctrl+c while ModeLogin: the
// wizard closes with a "cancelled" notice, mirroring cancelCompact.
func TestCancelLoginClosesWizard(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeLogin
	root.login = loginState{waiting: true, code: LoginDeviceCode{UserCode: "X"}}
	cancelled := false
	root.loginCancel = func() { cancelled = true }

	m, cmd := root.cancelLogin()
	if cmd != nil {
		t.Errorf("cancel is synchronous: want a nil cmd, got %v", cmd)
	}
	if !cancelled {
		t.Fatal("expected loginCancel to be called")
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "cancelado") {
		t.Fatalf("expected a cancellation notice, got %v", got.transcript)
	}
}
