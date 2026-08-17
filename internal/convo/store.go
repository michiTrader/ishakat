package convo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// StoreSchema es la versión del formato del archivo de sesión. Va en la
// cabecera para que una versión futura sepa qué está leyendo.
const StoreSchema = 1

// Header es la primera línea de cada archivo de sesión (§10).
type Header struct {
	Schema    int       `json:"schema"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`

	// Path es la ruta del archivo en disco. La rellena el almacén al cargar o
	// listar; no se serializa porque el archivo puede moverse.
	Path string `json:"-"`
}

// record es una línea del JSONL. Cabecera, mensaje o misión, nunca más de
// uno de los tres (§21.16 decisión 3: la misión es "a new event kind, not a
// sidecar file").
type record struct {
	Type    string        `json:"type"`
	Header  *Header       `json:"header,omitempty"`
	Message *Message      `json:"message,omitempty"`
	Mission *MissionEvent `json:"mission,omitempty"`
}

const (
	recHeader  = "header"
	recMessage = "message"
	recMission = "mission"
)

// ErrNotFound se devuelve cuando el id de sesión no existe en el directorio.
var ErrNotFound = errors.New("convo: sesión no encontrada")

// Store es el almacén de sesiones: un archivo JSONL por sesión, append-only.
//
// El único punto que rompe el append es SetTitle, que reescribe el archivo.
// Es deliberado y raro: pasa una vez por sesión cuando autoname le pone
// nombre. Los mensajes nunca reescriben nada.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore prepara el directorio de sesiones.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("convo: NewStore necesita un directorio")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("convo: no se pudo preparar %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir es el directorio del almacén.
func (s *Store) Dir() string { return s.dir }

// NewID genera un identificador ordenable por tiempo, como el de §10:
// 2026-07-30T14-02-11-a3f9.
func NewID(now time.Time) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return now.UTC().Format("2006-01-02T15-04-05") + "-" + string(b)
}

func (s *Store) path(id string) string {
	id = strings.TrimSuffix(id, ".jsonl")
	// Un id no puede salir del directorio del almacén.
	id = strings.ReplaceAll(id, string(os.PathSeparator), "_")
	id = strings.ReplaceAll(id, "..", "_")
	return filepath.Join(s.dir, id+".jsonl")
}

// New crea una sesión vacía con su cabecera ya escrita.
func (s *Store) New(title, model string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if strings.TrimSpace(title) == "" {
		title = "conversación nueva"
	}
	c := &Conversation{Header: Header{
		Schema:    StoreSchema,
		ID:        NewID(now),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
	}}
	c.Path = s.path(c.ID)

	f, err := os.OpenFile(c.Path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("convo: no se pudo crear %s: %w", c.Path, err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(record{Type: recHeader, Header: &c.Header}); err != nil {
		return nil, fmt.Errorf("convo: error escribiendo cabecera: %w", err)
	}
	return c, nil
}

// Append anexa un mensaje al archivo de la sesión. Se llama cuando el mensaje
// está completo, nunca durante el streaming (§10).
func (s *Store) Append(id string, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m.Ts.IsZero() {
		m.Ts = time.Now()
	}
	p := s.path(id)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return fmt.Errorf("convo: no se pudo abrir %s: %w", p, err)
	}
	defer f.Close()

	line, err := json.Marshal(record{Type: recMessage, Message: &m})
	if err != nil {
		return fmt.Errorf("convo: error serializando mensaje: %w", err)
	}
	// Una sola escritura por línea: si el proceso muere a mitad, se pierde
	// como máximo la última línea, y Load la descarta sin ruido.
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("convo: error anexando mensaje: %w", err)
	}
	return nil
}

// AppendMission anexa una resolución de misión/alcance de herramientas ya
// confirmada (§21.16 decisión 3) al archivo de la sesión. Mismo patrón que
// Append: una sola escritura por línea, para que un proceso muerto a mitad
// pierda como máximo esa línea.
func (s *Store) AppendMission(id string, ev MissionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.Ts.IsZero() {
		ev.Ts = time.Now()
	}
	p := s.path(id)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return fmt.Errorf("convo: no se pudo abrir %s: %w", p, err)
	}
	defer f.Close()

	line, err := json.Marshal(record{Type: recMission, Mission: &ev})
	if err != nil {
		return fmt.Errorf("convo: error serializando misión: %w", err)
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("convo: error anexando misión: %w", err)
	}
	return nil
}

// Load lee la sesión completa.
//
// Tolerancia a truncamiento: si la última línea quedó a medias (proceso muerto
// durante un append, batería agotada en el celular), se descarta y se
// devuelven todos los mensajes anteriores. Perder el último turno es
// aceptable; perder la conversación no lo es.
func (s *Store) Load(id string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(id)
}

func (s *Store) load(id string) (*Conversation, error) {
	p := s.path(id)
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("convo: no se pudo leer %s: %w", p, err)
	}

	c := &Conversation{}
	c.Path = p
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // los adjuntos base64 son largos

	// Si el archivo no termina en \n, la última línea está truncada.
	truncatedTail := len(raw) > 0 && raw[len(raw)-1] != '\n'
	var lines [][]byte
	for sc.Scan() {
		lines = append(lines, bytes.TrimSpace(sc.Bytes()))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("convo: error leyendo %s: %w", p, err)
	}
	if truncatedTail && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Línea corrupta en medio del archivo: se salta. El JSONL
			// existe justamente para que un byte malo no se lleve el resto.
			c.Corrupt++
			continue
		}
		switch rec.Type {
		case recHeader:
			if rec.Header != nil {
				h := *rec.Header
				h.Path = p
				c.Header = h
			}
		case recMessage:
			if rec.Message != nil {
				c.Messages = append(c.Messages, *rec.Message)
				if rec.Message.Ts.After(c.UpdatedAt) {
					c.UpdatedAt = rec.Message.Ts
				}
			}
		case recMission:
			if rec.Mission != nil {
				c.Missions = append(c.Missions, *rec.Mission)
			}
		default:
			c.Corrupt++
		}
	}

	if c.ID == "" {
		return nil, fmt.Errorf("convo: %s no tiene cabecera válida", p)
	}
	return c, nil
}

// List devuelve las cabeceras de todas las sesiones, más recientes primero.
// Lee únicamente la primera línea de cada archivo (§10): con doscientas
// sesiones guardadas, /resume tiene que abrir el menú al instante.
func (s *Store) List() ([]Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("convo: no se pudo listar %s: %w", s.dir, err)
	}

	var out []Header
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		h, err := readHeader(p)
		if err != nil {
			continue // un archivo roto no rompe el listado
		}
		// UpdatedAt sale del mtime: es exacto porque el último append es la
		// última escritura, y evita leer el archivo entero.
		if fi, err := e.Info(); err == nil {
			h.UpdatedAt = fi.ModTime()
		}
		out = append(out, h)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func readHeader(path string) (Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Header{}, fmt.Errorf("convo: %s está vacío", path)
	}
	var rec record
	if err := json.Unmarshal(bytes.TrimSpace(line), &rec); err != nil {
		return Header{}, fmt.Errorf("convo: cabecera ilegible en %s: %w", path, err)
	}
	if rec.Type != recHeader || rec.Header == nil {
		return Header{}, fmt.Errorf("convo: %s no empieza por cabecera", path)
	}
	h := *rec.Header
	h.Path = path
	return h, nil
}

// SetTitle cambia el título. Reescribe el archivo, que es la única operación
// que no es append; pasa una vez por sesión cuando autoname la nombra.
func (s *Store) SetTitle(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.load(id)
	if err != nil {
		return err
	}
	c.Title = title
	return s.rewrite(c)
}

// rewrite deja el archivo consistente escribiendo primero un temporal y
// renombrando encima. Un rename es atómico en el mismo directorio, así que un
// corte de energía deja o el archivo viejo o el nuevo, nunca una mezcla.
func (s *Store) rewrite(c *Conversation) error {
	p := s.path(c.ID)
	tmp, err := os.CreateTemp(s.dir, ".tmp-*.jsonl")
	if err != nil {
		return fmt.Errorf("convo: no se pudo crear temporal: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("convo: chmod del temporal: %w", err)
	}
	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	if err := enc.Encode(record{Type: recHeader, Header: &c.Header}); err != nil {
		tmp.Close()
		return fmt.Errorf("convo: error reescribiendo cabecera: %w", err)
	}
	for i := range c.Messages {
		if err := enc.Encode(record{Type: recMessage, Message: &c.Messages[i]}); err != nil {
			tmp.Close()
			return fmt.Errorf("convo: error reescribiendo mensaje %d: %w", i, err)
		}
	}
	for i := range c.Missions {
		if err := enc.Encode(record{Type: recMission, Mission: &c.Missions[i]}); err != nil {
			tmp.Close()
			return fmt.Errorf("convo: error reescribiendo misión %d: %w", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("convo: error volcando temporal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("convo: error cerrando temporal: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("convo: error reemplazando %s: %w", p, err)
	}
	return nil
}

// Save reescribe la sesión entera. Lo usa /compact, que anexa un resumen y
// puede querer consolidar; el camino normal es Append.
func (s *Store) Save(c *Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewrite(c)
}

// Delete borra una sesión.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("convo: error eliminando %s: %w", id, err)
	}
	return nil
}

// Rotate conserva las keepLast sesiones más recientes y borra el resto
// (session.keep_last de la configuración). Devuelve cuántas borró.
func (s *Store) Rotate(keepLast int) (int, error) {
	if keepLast <= 0 {
		return 0, nil
	}
	list, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(list) <= keepLast {
		return 0, nil
	}
	n := 0
	for _, h := range list[keepLast:] {
		if err := s.Delete(h.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Latest devuelve la sesión más reciente, si existe.
func (s *Store) Latest() (*Conversation, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return s.Load(list[0].ID)
}
