package convo_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// El cierre del Paso 2: veinte mensajes ida y vuelta, incluyendo un bloque de
// imagen y uno de resumen.
func TestRoundTripVeinteMensajes(t *testing.T) {
	st, err := convo.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	c, err := st.New("prueba", "omniroute/auto/coding")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := make([]convo.Message, 0, 20)
	for i := 0; i < 18; i++ {
		var m convo.Message
		if i%2 == 0 {
			m = convo.User(fmt.Sprintf("pregunta %d", i))
		} else {
			m = convo.Assistant(fmt.Sprintf("respuesta %d", i), "omniroute/anthropic/claude-sonnet-4-5")
			m.Usage = &convo.Usage{In: 10 + i, Out: 20 + i, Reasoning: i, CacheRead: 2}
		}
		want = append(want, m)
	}

	img := convo.NewMessage(convo.RoleUser,
		convo.TextBlock("mirá este gráfico"),
		convo.ImageBlock("image/png", []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}, "grafico.png"),
	)
	want = append(want, img)

	sum := convo.NewMessage(convo.RoleAssistant, convo.SummaryBlock("resumen de los primeros turnos", []int{0, 1, 2, 3}))
	sum.Model = "omniroute/auto/cheap"
	want = append(want, sum)

	for i, m := range want {
		if err := st.Append(c.ID, m); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Messages) != len(want) {
		t.Fatalf("esperados %d mensajes, obtenidos %d", len(want), len(got.Messages))
	}
	if got.Title != "prueba" || got.Model != "omniroute/auto/coding" || got.Schema != convo.StoreSchema {
		t.Errorf("cabecera no sobrevivió al viaje: %+v", got.Header)
	}

	for i := range want {
		w, g := want[i], got.Messages[i]
		if w.Role != g.Role || w.Text() != g.Text() || w.Model != g.Model {
			t.Errorf("mensaje %d difiere:\n  want %+v\n  got  %+v", i, w, g)
		}
		if len(w.Blocks) != len(g.Blocks) {
			t.Errorf("mensaje %d: esperados %d bloques, obtenidos %d", i, len(w.Blocks), len(g.Blocks))
		}
	}

	// El bloque de imagen conserva bytes y mime.
	gi := got.Messages[18]
	var found bool
	for _, blk := range gi.Blocks {
		if blk.Kind == convo.BlockImage {
			found = true
			if blk.Mime != "image/png" || len(blk.Data) != 6 || blk.Data[0] != 0x89 {
				t.Errorf("bloque de imagen corrupto: %+v", blk)
			}
		}
	}
	if !found {
		t.Error("el bloque de imagen no sobrevivió")
	}

	// El bloque de resumen conserva qué reemplaza.
	gs := got.Messages[19]
	if !gs.Has(convo.BlockSummary) {
		t.Fatal("el bloque de resumen no sobrevivió")
	}
	if gs.Blocks[0].Replaces == nil || len(gs.Blocks[0].Replaces) != 4 {
		t.Errorf("Replaces perdido: %+v", gs.Blocks[0])
	}

	// Active() excluye lo que el resumen reemplazó.
	if n := len(got.Active()); n != len(want)-4 {
		t.Errorf("Active(): esperados %d, obtenidos %d", len(want)-4, n)
	}

	// El usage total se acumula.
	if u := got.Usage(); u.Total() == 0 {
		t.Error("Usage() devolvió cero con mensajes que traen usage")
	}
}

// Truncar el archivo a mitad de una línea no puede perder los mensajes
// anteriores ni devolver un error fatal.
func TestLoadTolerToTruncamiento(t *testing.T) {
	dir := t.TempDir()
	st, _ := convo.NewStore(dir)
	c, _ := st.New("truncada", "m")

	for i := 0; i < 6; i++ {
		if err := st.Append(c.ID, convo.User(fmt.Sprintf("mensaje %d", i))); err != nil {
			t.Fatal(err)
		}
	}

	p := filepath.Join(dir, c.ID+".jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Cortar a mitad de la última línea.
	lastNL := strings.LastIndexByte(string(raw[:len(raw)-1]), '\n')
	cut := raw[:lastNL+1+10] // deja 10 bytes de la última línea, sin \n
	if err := os.WriteFile(p, cut, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load con cola truncada devolvió error: %v", err)
	}
	if len(got.Messages) != 5 {
		t.Fatalf("esperados 5 mensajes intactos, obtenidos %d", len(got.Messages))
	}
	if got.Messages[4].Text() != "mensaje 4" {
		t.Errorf("último mensaje intacto equivocado: %q", got.Messages[4].Text())
	}
}

// Una línea corrupta en el medio se salta y se cuenta, no aborta la carga.
func TestLoadSaltaLineaCorrupta(t *testing.T) {
	dir := t.TempDir()
	st, _ := convo.NewStore(dir)
	c, _ := st.New("corrupta", "m")
	_ = st.Append(c.ID, convo.User("uno"))

	p := filepath.Join(dir, c.ID+".jsonl")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{esto-no-es-json\n")
	_ = f.Close()
	_ = st.Append(c.ID, convo.User("dos"))

	got, err := st.Load(c.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("esperados 2 mensajes, obtenidos %d", len(got.Messages))
	}
	if got.Corrupt != 1 {
		t.Errorf("esperada 1 línea corrupta contabilizada, obtenidas %d", got.Corrupt)
	}
}

// List lee solo la primera línea de cada archivo y ordena por reciente.
func TestListSoloCabeceras(t *testing.T) {
	dir := t.TempDir()
	st, _ := convo.NewStore(dir)

	a, _ := st.New("primera", "m1")
	time.Sleep(15 * time.Millisecond)
	b, _ := st.New("segunda", "m2")
	time.Sleep(15 * time.Millisecond)
	if err := st.Append(a.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}

	list, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperadas 2 sesiones, obtenidas %d", len(list))
	}
	// a acaba de recibir un mensaje, así que va primero.
	if list[0].ID != a.ID {
		t.Errorf("orden equivocado: primero %s, esperado %s", list[0].ID, a.ID)
	}
	if list[1].Title != "segunda" || list[1].ID != b.ID {
		t.Errorf("cabecera equivocada: %+v", list[1])
	}
	// Un archivo suelto que no es JSONL no debe romper el listado.
	if err := os.WriteFile(filepath.Join(dir, "basura.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vacia.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if list2, err := st.List(); err != nil || len(list2) != 2 {
		t.Errorf("List con basura al lado: %d sesiones, err=%v", len(list2), err)
	}
}

func TestSetTitleYRotate(t *testing.T) {
	st, _ := convo.NewStore(t.TempDir())
	c, _ := st.New("sin nombre", "m")
	_ = st.Append(c.ID, convo.User("hola"))

	if err := st.SetTitle(c.ID, "optimizar consulta SQL"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	list, _ := st.List()
	if len(list) != 1 || list[0].Title != "optimizar consulta SQL" {
		t.Fatalf("el título nuevo no se ve en List: %+v", list)
	}
	got, _ := st.Load(c.ID)
	if len(got.Messages) != 1 || got.Messages[0].Text() != "hola" {
		t.Errorf("SetTitle perdió mensajes: %+v", got.Messages)
	}

	for i := 0; i < 4; i++ {
		time.Sleep(2 * time.Millisecond)
		if _, err := st.New(fmt.Sprintf("s%d", i), "m"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.Rotate(2)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if n != 3 {
		t.Errorf("Rotate(2) sobre 5 sesiones debía borrar 3, borró %d", n)
	}
	if list, _ := st.List(); len(list) != 2 {
		t.Errorf("después de rotar quedan %d sesiones", len(list))
	}
}

func TestSesionInexistente(t *testing.T) {
	st, _ := convo.NewStore(t.TempDir())
	if _, err := st.Load("no-existe"); err == nil {
		t.Fatal("se esperaba error al cargar una sesión inexistente")
	}
	if err := st.Append("no-existe", convo.User("x")); err == nil {
		t.Fatal("se esperaba error al anexar a una sesión inexistente")
	}
}

func TestBlockKindJSONLegible(t *testing.T) {
	b, err := json.Marshal(convo.SummaryBlock("x", []int{1}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"summary"`) {
		t.Errorf("el JSON debería nombrar el tipo de bloque: %s", b)
	}
	// Y debe aceptar el entero por compatibilidad.
	var blk convo.Block
	if err := json.Unmarshal([]byte(`{"kind":3,"text":"r"}`), &blk); err != nil {
		t.Fatalf("no aceptó el entero: %v", err)
	}
	if blk.Kind != convo.BlockToolResult {
		t.Errorf("kind 3 debería ser tool_result, es %v", blk.Kind)
	}
	if err := json.Unmarshal([]byte(`{"kind":"inventado"}`), &blk); err == nil {
		t.Error("un tipo inventado debería fallar")
	}
}

func TestAppendTextCoalesce(t *testing.T) {
	m := convo.NewMessage(convo.RoleAssistant)
	m.AppendText("Hola")
	m.AppendText(", ")
	m.AppendText("mundo")
	if len(m.Blocks) != 1 {
		t.Fatalf("el streaming debería coalescer en un bloque, hay %d", len(m.Blocks))
	}
	if m.Text() != "Hola, mundo" {
		t.Errorf("texto mal armado: %q", m.Text())
	}
	m.AppendReasoning("pensando")
	m.AppendText("y sigo")
	if len(m.Blocks) != 3 {
		t.Fatalf("cambiar de canal debería abrir bloque nuevo, hay %d", len(m.Blocks))
	}
	if m.Reasoning() != "pensando" {
		t.Errorf("razonamiento mal separado: %q", m.Reasoning())
	}
	if !strings.Contains(m.Text(), "y sigo") || strings.Contains(m.Text(), "pensando") {
		t.Errorf("Text() no debe incluir razonamiento: %q", m.Text())
	}
}
