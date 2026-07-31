package fake_test

import (
	"context"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/provider/openai"
)

// El proveedor falso tiene que cumplir el mismo contrato que el de verdad; si
// no, los tests que lo usen probarán una fantasía. Estos dos casos verifican
// justo eso.

func TestFakeCumpleLaInterfaz(t *testing.T) {
	var _ provider.Provider = fake.New("x")
}

func TestFakeGarantizaDoneAlFinal(t *testing.T) {
	p := fake.Text("f", "ho", "la")
	p.Script = append(p.Script, fake.Usage(3, 2))

	ch, err := p.Stream(context.Background(), provider.Request{
		Model:    "m",
		Messages: []convo.Message{convo.User("hola")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text string
	var kinds []provider.EventKind
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == provider.EventDelta {
			text += ev.Text
		}
	}
	if text != "hola" {
		t.Errorf("texto mal emitido: %q", text)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != provider.EventDone {
		t.Errorf("EventDone debe cerrar el guion: %v", kinds)
	}
	if len(p.Turns()) != 1 || p.LastTurn().Model != "m" {
		t.Errorf("el turno debe quedar registrado: %+v", p.Turns())
	}
}

func TestSSEServerHablaElDialectoDeVerdad(t *testing.T) {
	// El servidor falso se prueba contra el adaptador real: si el fixture que
	// produce no lo entiende el parser de openai, no sirve para nada.
	srv := fake.SSEServer(fake.SSEOptions{
		Chunks: []string{
			fake.SSEDelta("hola "),
			fake.SSEDelta(`mundo "con comillas"`),
			fake.SSEDone(),
		},
	})
	defer srv.Close()

	p, err := provider.New(provider.Settings{ID: "fake", Kind: "openai", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{
		Model:    "m",
		Messages: []convo.Message{convo.User("hola")},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text string
	var errEv error
	for ev := range ch {
		switch ev.Kind {
		case provider.EventDelta:
			text += ev.Text
		case provider.EventError:
			errEv = ev.Err
		}
	}
	if errEv != nil {
		t.Fatalf("el stream del servidor falso debe cerrar limpio: %v", errEv)
	}
	if text != `hola mundo "con comillas"` {
		t.Errorf("texto mal escapado o mal leído: %q", text)
	}

	// Y el import de openai se usa también para dejar claro que este paquete
	// no depende de él: solo el test.
	_ = openai.ChatMessage{}
}
