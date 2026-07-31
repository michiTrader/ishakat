package openai

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

// Parser de Server-Sent Events (WHATWG / W3C EventSource), la parte del Paso 4
// donde se esconden los bugs.
//
// Las cuatro reglas que se respetan al pie de la letra, porque cada una
// corresponde a un fallo real observado en gateways distintos:
//
//  1. Un Read del socket NO trae un evento completo. Puede traer medio campo,
//     medio JSON, o tres eventos y la mitad del cuarto. El troceado en líneas
//     lo hace un bufio.Scanner con split function propia, y el troceado en
//     eventos lo hace el acumulador de campos: nunca se mira el tamaño del
//     Read.
//  2. Los separadores de línea válidos son \n, \r\n y \r a secas. Un \r al
//     final del búfer, con datos aún por llegar, es ambiguo: puede ser el
//     principio de \r\n. En ese caso se pide más datos en vez de emitir una
//     línea que después habría que corregir.
//  3. Una línea vacía despacha el evento. Varios campos data se concatenan
//     con \n entre ellos, no se sobrescriben.
//  4. Una línea que empieza con ':' es un comentario. Los gateways los usan
//     como keep-alive cada 15 segundos; tratarlos como datos rompe el JSON.
//
// Y una regla de cierre propia: si el flujo termina con un evento a medias
// —sin la línea vacía final— ese evento se descarta y Next devuelve
// errIncompleteEvent. Fingir que estaba completo produce JSON inválido; el
// nivel de arriba lo convierte en provider.ErrStreamTruncated y conserva lo
// que sí llegó.

// errIncompleteEvent indica que el flujo se cortó con campos a medio
// acumular. Es interno: stream.go lo traduce a provider.ErrStreamTruncated.
var errIncompleteEvent = errors.New("sse: el flujo terminó con un evento incompleto")

// maxEventBytes limita cuánto puede crecer una sola línea. Un chunk de
// streaming ronda los 300 bytes; un megabyte es tres mil veces más que el
// peor caso legítimo y evita que un servicio roto agote la memoria del
// teléfono.
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

	// Estado del evento en construcción.
	name  string
	data  []byte
	id    string
	retry int
	dirty bool // hay al menos un campo acumulado
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), maxEventBytes)
	sc.Split(scanSSELines)
	return &sseScanner{sc: sc}
}

// scanSSELines es la split function: devuelve una línea por cada \n, \r\n o
// \r, sin el terminador. Es igual a bufio.ScanLines salvo en el tratamiento
// del \r solitario, que ScanLines no reconoce como fin de línea.
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
			// Regla 2: \r al final del búfer con más datos en camino.
			// Pedir otro Read antes de decidir.
			return 0, nil, nil
		}
	}
	if atEOF {
		// Última línea sin terminador. El acumulador decidirá si el evento
		// quedó completo (no lo estará: falta la línea vacía).
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Next devuelve el siguiente evento completo.
//
// Devuelve io.EOF cuando el flujo terminó limpio, errIncompleteEvent si
// terminó con campos a medias, y el error del lector si la lectura falló.
func (s *sseScanner) Next() (sseEvent, error) {
	for s.sc.Scan() {
		line := s.sc.Bytes()

		// Línea vacía: despacha (regla 3).
		if len(line) == 0 {
			if !s.dirty {
				continue // separadores repetidos, o preámbulo del servidor
			}
			ev := sseEvent{Name: s.name, Data: s.data, ID: s.id, Retry: s.retry}
			s.reset()
			return ev, nil
		}

		// Comentario / keep-alive (regla 4).
		if line[0] == ':' {
			continue
		}

		field, value := splitField(line)
		switch string(field) {
		case "data":
			if s.data != nil {
				s.data = append(s.data, '\n')
			}
			// El token del scanner se reutiliza en la lectura siguiente: hay
			// que copiar, no referenciar.
			s.data = append(s.data, value...)
			s.dirty = true
		case "event":
			s.name = string(value)
			s.dirty = true
		case "id":
			// La especificación manda ignorar un id con byte nulo.
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
			// Campo desconocido: se ignora, como manda la especificación.
		}
	}

	if err := s.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return sseEvent{}, errors.New("sse: línea más larga que el límite de 1 MiB")
		}
		return sseEvent{}, err
	}
	if s.dirty {
		// Regla de cierre: evento a medias al llegar el EOF.
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
// del principio del valor, como manda la especificación. Una línea sin ':' es
// un campo con valor vacío.
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
