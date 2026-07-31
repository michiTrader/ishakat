package openai

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// chunkReader entrega el flujo en los trozos exactos que se le indican, uno
// por Read. Es la herramienta central de este archivo: sirve para reproducir
// el caso que rompe a la mayoría de los parsers de SSE, que es un evento
// partido a mitad de campo entre dos lecturas del socket.
type chunkReader struct {
	chunks []string
	i      int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	if n < len(r.chunks[r.i]) {
		r.chunks[r.i] = r.chunks[r.i][n:]
		return n, nil
	}
	r.i++
	return n, nil
}

// collect drena el scanner y devuelve los eventos y el error de cierre.
func collect(t *testing.T, r io.Reader) ([]sseEvent, error) {
	t.Helper()
	sc := newSSEScanner(r)
	var out []sseEvent
	for {
		ev, err := sc.Next()
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}

func TestSSESeparaEventosPorLineaVacia(t *testing.T) {
	in := "data: uno\n\ndata: dos\n\n"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("esperados 2 eventos, %d: %+v", len(evs), evs)
	}
	if string(evs[0].Data) != "uno" || string(evs[1].Data) != "dos" {
		t.Errorf("datos mal leídos: %q / %q", evs[0].Data, evs[1].Data)
	}
}

func TestSSEIgnoraComentariosYCamposDesconocidos(t *testing.T) {
	// Los comentarios son el keep-alive de los gateways: tratarlos como datos
	// rompería el JSON del chunk siguiente.
	in := ": ping\n:otra cosa\nfoo: bar\ndata: real\n\n"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 1 || string(evs[0].Data) != "real" {
		t.Fatalf("los comentarios no deben producir eventos: %+v", evs)
	}
}

func TestSSEAceptaLosTresTerminadoresDeLinea(t *testing.T) {
	// \n, \r\n y \r a secas son los tres separadores legales.
	in := "data: a\r\n\r\ndata: b\rdata: c\r\r"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("esperados 2 eventos, %d: %+v", len(evs), evs)
	}
	if string(evs[0].Data) != "a" {
		t.Errorf("CRLF mal manejado: %q", evs[0].Data)
	}
	if string(evs[1].Data) != "b\nc" {
		t.Errorf("varias líneas data se concatenan con \\n: %q", evs[1].Data)
	}
}

func TestSSECRPartidoEntreDosLecturas(t *testing.T) {
	// El caso ambiguo: el \r queda al final de una lectura y el \n llega en la
	// siguiente. Emitir la línea al ver el \r produciría un evento vacío de
	// más.
	r := &chunkReader{chunks: []string{"data: hola\r", "\n\r\n"}}
	evs, err := collect(t, r)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 1 || string(evs[0].Data) != "hola" {
		t.Fatalf("CRLF partido entre lecturas mal manejado: %+v", evs)
	}
}

func TestSSEEventoPartidoEnMuchasLecturas(t *testing.T) {
	// Ninguna de estas lecturas contiene un evento completo.
	r := &chunkReader{chunks: []string{
		"eve", "nt: mensaje\nda", "ta: {\"a\":", "1}\nid: 7\nretry: 250\n", "\n",
	}}
	evs, err := collect(t, r)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("esperado 1 evento, %d: %+v", len(evs), evs)
	}
	ev := evs[0]
	if ev.Name != "mensaje" || string(ev.Data) != `{"a":1}` || ev.ID != "7" || ev.Retry != 250 {
		t.Errorf("campos mal ensamblados: %+v", ev)
	}
}

func TestSSEValorSinEspacioYValorVacio(t *testing.T) {
	in := "data:pegado\n\ndata:\n\n"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("esperados 2 eventos, %d: %+v", len(evs), evs)
	}
	if string(evs[0].Data) != "pegado" {
		t.Errorf("solo se quita un espacio, y solo si está: %q", evs[0].Data)
	}
	if len(evs[1].Data) != 0 {
		t.Errorf("un data vacío es un evento válido con datos vacíos: %q", evs[1].Data)
	}
}

func TestSSEEventoIncompletoAlFinal(t *testing.T) {
	// Sin la línea vacía final el evento no está despachado: la especificación
	// manda descartarlo, y para ishakat eso significa "stream cortado".
	in := "data: completo\n\ndata: a med"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, errIncompleteEvent) {
		t.Fatalf("se esperaba errIncompleteEvent, hubo %v", err)
	}
	if len(evs) != 1 || string(evs[0].Data) != "completo" {
		t.Fatalf("lo completo debe conservarse: %+v", evs)
	}
}

func TestSSELineaMasLargaQueElLimite(t *testing.T) {
	in := "data: " + strings.Repeat("x", maxEventBytes+10) + "\n\n"
	_, err := collect(t, strings.NewReader(in))
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("una línea gigante debe fallar explícitamente, hubo %v", err)
	}
	if !strings.Contains(err.Error(), "MiB") {
		t.Errorf("el error debe explicar el límite: %v", err)
	}
}

func TestSSEPreambuloYSeparadoresRepetidos(t *testing.T) {
	in := "\n\n\ndata: uno\n\n\n\ndata: dos\n\n"
	evs, err := collect(t, strings.NewReader(in))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("se esperaba EOF limpio, hubo %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("las líneas vacías de más no crean eventos: %+v", evs)
	}
}
