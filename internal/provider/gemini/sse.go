package gemini

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

// Parser de Server-Sent Events, copiado a propósito de anthropic/sse.go
// (que a su vez lo copia de openai/sse.go) en vez de compartido: el mismo
// razonamiento documentado en esos dos archivos — código que cambia junto
// con tanta frecuencia como cambia nunca (el propio WHATWG SSE spec), y
// extraerlo a un tercer paquete solo para ahorrarse ~150 líneas idénticas no
// vale la indirección. Gemini no manda un campo `event:` con nombre (a
// diferencia de Anthropic): cada línea `data:` lleva un
// wireGenerateContentResponse completo y este parser genérico no necesita
// ningún cambio para eso — sseEvent.Name simplemente queda vacío siempre.

// errIncompleteEvent indica que el flujo se cortó con campos a medio
// acumular. Es interno: stream.go lo traduce a provider.ErrStreamTruncated.
var errIncompleteEvent = errors.New("sse: el flujo terminó con un evento incompleto")

// maxEventBytes limita cuánto puede crecer una sola línea.
const maxEventBytes = 1 << 20

// sseEvent es un evento ya ensamblado.
type sseEvent struct {
	Name  string // campo "event"; vacío equivale a "message"
	Data  []byte // campos "data" concatenados con \n
	ID    string // campo "id"
	Retry int    // campo "retry", en milisegundos
}

// sseScanner lee eventos de un flujo SSE.
type sseScanner struct {
	sc *bufio.Scanner

	name  string
	data  []byte
	id    string
	retry int
	dirty bool
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), maxEventBytes)
	sc.Split(scanSSELines)
	return &sseScanner{sc: sc}
}

// scanSSELines devuelve una línea por cada \n, \r\n o \r, sin el terminador.
func scanSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			return 0, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Next devuelve el siguiente evento completo.
func (s *sseScanner) Next() (sseEvent, error) {
	for s.sc.Scan() {
		line := s.sc.Bytes()

		if len(line) == 0 {
			if !s.dirty {
				continue
			}
			ev := sseEvent{Name: s.name, Data: s.data, ID: s.id, Retry: s.retry}
			s.reset()
			return ev, nil
		}

		if line[0] == ':' {
			continue
		}

		field, value := splitField(line)
		switch string(field) {
		case "data":
			if s.data != nil {
				s.data = append(s.data, '\n')
			}
			s.data = append(s.data, value...)
			s.dirty = true
		case "event":
			s.name = string(value)
			s.dirty = true
		case "id":
			if !bytes.ContainsRune(value, 0) {
				s.id = string(value)
			}
			s.dirty = true
		case "retry":
			if n, err := strconv.Atoi(string(value)); err == nil && n >= 0 {
				s.retry = n
			}
			s.dirty = true
		default:
		}
	}

	if err := s.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return sseEvent{}, errors.New("sse: línea más larga que el límite de 1 MiB")
		}
		return sseEvent{}, err
	}
	if s.dirty {
		s.reset()
		return sseEvent{}, errIncompleteEvent
	}
	return sseEvent{}, io.EOF
}

func (s *sseScanner) reset() {
	s.name = ""
	s.data = nil
	s.id = ""
	s.retry = 0
	s.dirty = false
}

// splitField parte "campo: valor" por el primer ':' y quita un único espacio
// del principio del valor. Una línea sin ':' es un campo con valor vacío.
func splitField(line []byte) (field, value []byte) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return line, nil
	}
	field = line[:i]
	value = line[i+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return field, value
}
