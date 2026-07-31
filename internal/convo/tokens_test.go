package convo_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

func TestEstimateText(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		min, max int
	}{
		{"vacío", "", 0, 0},
		{"una palabra", "hola", 1, 2},
		{"prosa", strings.Repeat("palabra ", 50), 90, 110}, // 400 chars ≈ 100 tok
		{"código", "```go\n" + strings.Repeat("x := 1\n", 30) + "```", 60, 90},
		{"cjk", strings.Repeat("漢", 60), 45, 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := convo.EstimateText(c.in)
			if got < c.min || got > c.max {
				t.Errorf("EstimateText = %d, esperado entre %d y %d", got, c.min, c.max)
			}
		})
	}
}

func TestEstimateCodigoMasCaroQueProsa(t *testing.T) {
	n := 600
	prosa := strings.Repeat("a", n)
	codigo := "```\n" + strings.Repeat("a", n) + "\n```"
	if convo.EstimateText(codigo) <= convo.EstimateText(prosa) {
		t.Errorf("el código debe estimar más caro: prosa=%d código=%d",
			convo.EstimateText(prosa), convo.EstimateText(codigo))
	}
}

func TestEstimateMessageCuentaImagenes(t *testing.T) {
	txt := convo.User("hola")
	img := convo.NewMessage(convo.RoleUser,
		convo.TextBlock("hola"),
		convo.ImageBlock("image/png", []byte("x"), "a.png"))
	if convo.EstimateMessage(img) <= convo.EstimateMessage(txt)+100 {
		t.Errorf("una imagen debe pesar bastante más: %d vs %d",
			convo.EstimateMessage(img), convo.EstimateMessage(txt))
	}
}

func TestContextTokensUsaUsageReal(t *testing.T) {
	c := &convo.Conversation{}
	c.Add(convo.User("pregunta larga " + strings.Repeat("x", 400)))
	a := convo.Assistant("respuesta", "m")
	a.Usage = &convo.Usage{In: 5000, Out: 100}
	c.Add(a)
	c.Add(convo.User("otra"))

	got := c.ContextTokens()
	if got < 5100 {
		t.Errorf("ContextTokens debería partir del usage real (5100+), dio %d", got)
	}
	if got > 5200 {
		t.Errorf("ContextTokens se pasó de largo: %d", got)
	}
}

func TestContextTokensSinUsage(t *testing.T) {
	c := &convo.Conversation{}
	c.Add(convo.User(strings.Repeat("a", 400)))
	got := c.ContextTokens()
	if got < 90 || got > 120 {
		t.Errorf("sin usage debe estimar ~100 tokens, dio %d", got)
	}
}

func TestRatiosAprendenYPersisten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ratios.json")
	r := convo.LoadRatios(p)

	if got := r.For("gpt-5"); got < 3.9 || got > 4.1 {
		t.Errorf("sin datos el ratio base debe ser 4, dio %v", got)
	}
	// 3000 caracteres costaron 1000 tokens: ratio 3.
	r.Observe("gpt-5", 3000, 1000)
	if got := r.For("gpt-5"); got < 2.9 || got > 3.1 {
		t.Errorf("ratio aprendido debería ser ~3, dio %v", got)
	}
	// Con ratio 3, una estimación hecha con ratio 4 debe subir un tercio.
	if got := r.Correct("gpt-5", 300); got < 390 || got > 410 {
		t.Errorf("Correct debería dar ~400, dio %d", got)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	r2 := convo.LoadRatios(p)
	if got := r2.For("gpt-5"); got < 2.9 || got > 3.1 {
		t.Errorf("el ratio no persistió: %v", got)
	}
	// Valores absurdos se ignoran.
	r2.Observe("raro", 1, 1000)
	if got := r2.For("raro"); got < 3.9 || got > 4.1 {
		t.Errorf("un ratio absurdo debe caer al base, dio %v", got)
	}
	// Un archivo ilegible no debe romper nada.
	bad := convo.LoadRatios(filepath.Join(t.TempDir(), "no-existe.json"))
	if got := bad.For("x"); got <= 0 {
		t.Error("LoadRatios sobre un archivo ausente debe devolver el base")
	}
}

func TestPlanCompact(t *testing.T) {
	msgs := []convo.Message{
		convo.System("sos un asistente"),
	}
	for i := 0; i < 6; i++ {
		msgs = append(msgs, convo.User("p"), convo.Assistant("r", "m"))
	}

	p := convo.PlanCompact(msgs, 2)
	// 6 turnos, se conservan los 2 últimos → se resumen los 4 primeros = 8 mensajes.
	if len(p.Replace) != 8 {
		t.Fatalf("esperados 8 mensajes a resumir, hay %d (%v)", len(p.Replace), p.Replace)
	}
	if len(p.System) != 1 || p.System[0] != 0 {
		t.Errorf("el mensaje de sistema debe quedar fuera: %v", p.System)
	}
	for _, i := range p.Replace {
		if i == 0 {
			t.Error("el sistema no se puede resumir")
		}
	}
	if p.Tokens <= 0 {
		t.Error("el plan debe estimar tokens")
	}
	if len(p.Keep) != 4 {
		t.Errorf("esperados 4 mensajes conservados, hay %d", len(p.Keep))
	}

	// Con menos turnos que el mínimo, no hay nada que hacer.
	if q := convo.PlanCompact(msgs[:3], 4); !q.Empty() {
		t.Errorf("no debería compactar: %+v", q)
	}
}

func TestApplySummaryEsReversible(t *testing.T) {
	c := &convo.Conversation{}
	for i := 0; i < 8; i++ {
		c.Add(convo.User("p"))
		c.Add(convo.Assistant("r", "m"))
	}
	before := len(c.Messages)

	p := convo.PlanCompact(c.Messages, 2)
	idx := c.ApplySummary(p, "resumen", "omniroute/auto/cheap")
	if idx < 0 {
		t.Fatal("ApplySummary no aplicó")
	}
	// Nada se borró: el historial completo sigue ahí.
	if len(c.Messages) != before+1 {
		t.Fatalf("ApplySummary debe anexar, no reescribir: %d → %d", before, len(c.Messages))
	}
	// Pero el contexto activo es más chico.
	if len(c.Active()) >= before {
		t.Errorf("Active() debería encogerse: %d de %d", len(c.Active()), before)
	}
	// Y una segunda pasada no vuelve a resumir lo mismo.
	q := convo.PlanCompact(c.Messages, 2)
	for _, i := range q.Replace {
		for _, j := range p.Replace {
			if i == j {
				t.Fatalf("el mensaje %d se resumiría dos veces", i)
			}
		}
	}
}

func TestNeedsCompactYDropOldest(t *testing.T) {
	if !convo.NeedsCompact(85_000, 100_000, 80) {
		t.Error("85k de 100k con umbral 80% debe disparar")
	}
	if convo.NeedsCompact(70_000, 100_000, 80) {
		t.Error("70k de 100k no debe disparar")
	}
	if convo.NeedsCompact(1, 0, 80) {
		t.Error("sin ventana conocida no se puede decidir")
	}

	msgs := []convo.Message{convo.System("sys")}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, convo.User(strings.Repeat("a", 400)))
	}
	drop := convo.DropOldest(msgs, 300)
	if len(drop) == 0 {
		t.Fatal("DropOldest no descartó nada")
	}
	for _, i := range drop {
		if i == 0 {
			t.Error("DropOldest no puede descartar el mensaje de sistema")
		}
	}
}
