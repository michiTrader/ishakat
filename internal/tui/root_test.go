package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
)

func newTestRoot() tea.Model {
	r := tui.NewRoot(tui.Options{
		Version: "0.1.0",
		CWD:     "/home/user/api",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		NoTTY:   true, // evita cursor/banner en el camino de test
	})
	m, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func keyMsg(text string, code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: text, Code: code, Mod: mod}
}

func typeText(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(keyMsg(string(r), r, 0))
	}
	return m
}

func TestSubmitPasaAModeBusy(t *testing.T) {
	m := newTestRoot()
	m = typeText(m, "hola")
	m, cmd := m.Update(keyMsg("", tea.KeyEnter, 0))
	if cmd == nil {
		t.Fatal("submit debería devolver comandos (tick de stream y anim)")
	}
	if !strings.Contains(m.View().Content, "pensando") {
		t.Errorf("tras submit debería verse la línea de 'pensando': %q", m.View().Content)
	}
	// RC-2: ModeBusy still draws the input box, so the hardware cursor stays
	// inside it. That is not typing-while-busy (updateBusy still swallows keys).
	if m.View().Cursor == nil {
		t.Error("ModeBusy must keep the terminal cursor inside the still-drawn input (RC-2)")
	}
}

func TestEnterConInputVacioNoHaceNada(t *testing.T) {
	m := newTestRoot()
	m, cmd := m.Update(keyMsg("", tea.KeyEnter, 0))
	if cmd != nil {
		t.Error("enter con el input vacío no debería producir ningún comando")
	}
	if strings.Contains(m.View().Content, "pensando") {
		t.Error("sin texto no debería arrancar ningún turno")
	}
}

func TestDobleCtrlCSaleEnVentanaDeGracia(t *testing.T) {
	m := newTestRoot()
	m, cmd1 := m.Update(keyMsg("", 'c', tea.ModCtrl))
	if cmd1 == nil {
		t.Fatal("el primer ctrl+c debería armar la ventana de gracia")
	}
	m, cmd2 := m.Update(keyMsg("", 'c', tea.ModCtrl))
	if cmd2 == nil {
		t.Fatal("el segundo ctrl+c dentro de la ventana debería producir tea.Quit")
	}
	msg := cmd2()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("esperaba tea.QuitMsg, obtuve %T", msg)
	}
}

func TestCtrlCEnModeBusyCancelaEnVezDeSalir(t *testing.T) {
	m := newTestRoot()
	m = typeText(m, "hola")
	m, _ = m.Update(keyMsg("", tea.KeyEnter, 0))

	m, cmd := m.Update(keyMsg("", 'c', tea.ModCtrl))
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("un solo ctrl+c en ModeBusy nunca debe cerrar la aplicación")
		}
	}
}

func TestCtrlLLimpiaTranscript(t *testing.T) {
	m := newTestRoot()
	m, cmd := m.Update(keyMsg("", 'l', tea.ModCtrl))
	if cmd == nil {
		t.Fatal("ctrl+l debería devolver el comando ClearScreen")
	}
}

func TestWindowSizeMsgActualizaBreakpoint(t *testing.T) {
	m := newTestRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	view := m.View()
	// A 30 columnas (BPMinimo) el input no debe llevar caja de bordes.
	if strings.Contains(view.Content, "╭") {
		t.Errorf("en BPMinimo no debería dibujarse la caja de bordes: %q", view.Content)
	}
}

func TestModeChatEsElInicial(t *testing.T) {
	m := newTestRoot()
	view := m.View()
	if view.Cursor == nil {
		t.Error("en ModeChat el cursor real de terminal debería estar activo")
	}
}

// newCfgRoot is newTestRoot with a real Config.Keys. RC-1 lived in the
// gap between defaultMap (what newTestRoot exercises) and the shipped
// defaults file (what a real run loads through NewMap). A test that
// never sets Cfg cannot see that bug.
func newCfgRoot(keys config.Keys) tea.Model {
	r := tui.NewRoot(tui.Options{
		Version: "0.1.0",
		CWD:     "/home/user/api",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		NoTTY:   true,
		Cfg:     &config.Config{Keys: keys},
	})
	m, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func assertQuitOnNthCtrlC(t *testing.T, m tea.Model, n int) {
	t.Helper()
	var cmd tea.Cmd
	for i := 1; i <= n; i++ {
		m, cmd = m.Update(keyMsg("", 'c', tea.ModCtrl))
		if i < n {
			if cmd == nil && i == 1 {
				t.Fatalf("press %d of %d should arm the grace window", i, n)
			}
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); ok {
					t.Fatalf("press %d of %d must not quit yet", i, n)
				}
			}
			continue
		}
		if cmd == nil {
			t.Fatalf("press %d of %d should produce tea.Quit", i, n)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("press %d of %d: expected tea.QuitMsg, got %T", i, n, cmd())
		}
	}
}

// TestRC1LegacyMultiWordQuitLoadedThroughCfg is the case the original
// suite could not see: a real Config carrying the pre-RC-1 form
// quit = "ctrl+c ctrl+c" must still double-press to exit.
func TestRC1LegacyMultiWordQuitLoadedThroughCfg(t *testing.T) {
	m := newCfgRoot(config.Keys{Quit: "ctrl+c ctrl+c"})
	assertQuitOnNthCtrlC(t, m, 2)
}

// TestRC1ShippedQuitAndRepeatLoadedThroughCfg is the post-fix form the
// embedded defaults now ship. Same behaviour, different data.
func TestRC1ShippedQuitAndRepeatLoadedThroughCfg(t *testing.T) {
	m := newCfgRoot(config.Keys{Quit: "ctrl+c", QuitRepeat: 2})
	assertQuitOnNthCtrlC(t, m, 2)
}

func TestRC1QuitRepeatOneQuitsOnFirstPress(t *testing.T) {
	m := newCfgRoot(config.Keys{Quit: "ctrl+c", QuitRepeat: 1})
	assertQuitOnNthCtrlC(t, m, 1)
}
