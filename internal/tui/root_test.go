package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	// En ModeBusy el cursor real de terminal se apaga: no hay nada editable.
	if m.View().Cursor != nil {
		t.Error("en ModeBusy no debería haber cursor de edición activo")
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
