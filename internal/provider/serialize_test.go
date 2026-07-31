package provider_test

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

func TestFromConvoTextoPlano(t *testing.T) {
	msgs := []convo.Message{
		convo.System("sos conciso"),
		convo.User("hola"),
		convo.Assistant("qué tal", "m"),
	}
	out, deg := provider.FromConvo(msgs, provider.Caps{})
	if len(out) != 3 {
		t.Fatalf("esperados 3 mensajes, %d", len(out))
	}
	if out[0].Role != "system" || out[1].Content != "hola" {
		t.Errorf("traducción equivocada: %+v", out)
	}
	if deg.Any() {
		t.Errorf("no debería degradar nada: %+v", deg)
	}
}

func TestFromConvoDegradaImagenesYHerramientas(t *testing.T) {
	m := convo.NewMessage(convo.RoleUser,
		convo.TextBlock("mirá esto"),
		convo.ImageBlock("image/png", []byte("x"), "captura.png"),
	)
	tool := convo.NewMessage(convo.RoleAssistant,
		convo.Block{Kind: convo.BlockToolCall, Name: "grep", Args: []byte(`{"q":"x"}`)},
	)
	res := convo.NewMessage(convo.RoleTool,
		convo.Block{Kind: convo.BlockToolResult, Name: "grep", Text: "3 coincidencias"},
	)

	out, deg := provider.FromConvo([]convo.Message{m, tool, res}, provider.Caps{})
	if deg.ImagesDropped != 1 || deg.ToolsFlattened != 2 {
		t.Fatalf("degradación mal contada: %+v", deg)
	}
	if !deg.Any() || !strings.Contains(deg.Reason(), "imagen") {
		t.Errorf("Reason poco informativo: %q", deg.Reason())
	}
	if !strings.Contains(out[0].Content, "captura.png") {
		t.Errorf("la imagen debe anunciarse en el texto: %q", out[0].Content)
	}
	if !strings.Contains(out[1].Content, "grep") {
		t.Errorf("la llamada debe aplanarse legible: %q", out[1].Content)
	}
}

func TestFromConvoOmiteRazonamientoYMarcaAbortado(t *testing.T) {
	m := convo.NewMessage(convo.RoleAssistant,
		convo.ReasoningBlock("primero pienso"),
		convo.TextBlock("la respuesta a medias"),
	)
	m.Aborted = true

	out, deg := provider.FromConvo([]convo.Message{m}, provider.Caps{})
	if deg.ReasoningDropped != 1 {
		t.Errorf("el razonamiento debe omitirse: %+v", deg)
	}
	if strings.Contains(out[0].Content, "primero pienso") {
		t.Errorf("el razonamiento no se reenvía: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "interrumpida") {
		t.Errorf("un mensaje abortado debe declararlo: %q", out[0].Content)
	}
}

func TestFromConvoSaltaMensajesVacios(t *testing.T) {
	msgs := []convo.Message{
		convo.User(""),
		convo.NewMessage(convo.RoleAssistant, convo.ReasoningBlock("solo pensé")),
		convo.User("real"),
	}
	out, _ := provider.FromConvo(msgs, provider.Caps{})
	if len(out) != 1 || out[0].Content != "real" {
		t.Errorf("los mensajes sin contenido enviable se saltan: %+v", out)
	}
}

func TestFromConvoResumenSeEnvia(t *testing.T) {
	m := convo.NewMessage(convo.RoleAssistant, convo.SummaryBlock("hablamos de índices", []int{0, 1}))
	out, _ := provider.FromConvo([]convo.Message{m}, provider.Caps{})
	if len(out) != 1 || !strings.Contains(out[0].Content, "índices") {
		t.Fatalf("el resumen debe viajar: %+v", out)
	}
	if !strings.Contains(out[0].Content, "resumen") {
		t.Errorf("el resumen debe presentarse como tal: %q", out[0].Content)
	}
}
