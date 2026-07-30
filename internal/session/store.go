package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

type Store struct {
	dir string
	mu  sync.RWMutex
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		dir = xdg.SessionsDir()
	}
	if err := xdg.EnsureDir(dir); err != nil {
		return nil, fmt.Errorf("error preparando directorio de sesiones %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func generateID() string {
	now := time.Now().Format("20060102-150405")
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return fmt.Sprintf("%s-%s", now, string(b))
}

func (s *Store) sessionPath(id string) string {
	if !strings.HasSuffix(id, ".jsonl") {
		id = id + ".jsonl"
	}
	return filepath.Join(s.dir, id)
}

func (s *Store) NewSession(model string, title string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	id := generateID()
	if title == "" {
		title = "Nueva Conversación"
	}

	sess := &Session{
		Header: Header{
			ID:        id,
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
			Model:     model,
		},
		Messages: []Message{},
	}

	if err := s.writeFullSession(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) writeFullSession(sess *Session) error {
	path := s.sessionPath(sess.ID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("no se pudo abrir el archivo de sesión %s: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	
	hdrRec := LineRecord{
		Type:   RecordHeader,
		Header: &sess.Header,
	}
	if err := enc.Encode(hdrRec); err != nil {
		return fmt.Errorf("error escribiendo cabecera de sesión: %w", err)
	}

	for i := range sess.Messages {
		msgRec := LineRecord{
			Type:    RecordMessage,
			Message: &sess.Messages[i],
		}
		if err := enc.Encode(msgRec); err != nil {
			return fmt.Errorf("error escribiendo mensaje %d: %w", i, err)
		}
	}
	return nil
}

func (s *Store) AppendMessage(sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadUnlocked(sessionID)
	if err != nil {
		return err
	}

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	sess.Messages = append(sess.Messages, msg)
	sess.UpdatedAt = msg.Timestamp

	path := s.sessionPath(sessionID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("error abriendo sesión para append %s: %w", path, err)
	}
	defer f.Close()

	msgRec := LineRecord{
		Type:    RecordMessage,
		Message: &msg,
	}
	if err := json.NewEncoder(f).Encode(msgRec); err != nil {
		return fmt.Errorf("error haciendo append de mensaje: %w", err)
	}

	return s.updateHeaderUnlocked(&sess.Header)
}

func (s *Store) UpdateTitle(sessionID string, newTitle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadUnlocked(sessionID)
	if err != nil {
		return err
	}
	sess.Title = newTitle
	sess.UpdatedAt = time.Now()
	return s.writeFullSession(sess)
}

func (s *Store) updateHeaderUnlocked(hdr *Header) error {
	sess, err := s.loadUnlocked(hdr.ID)
	if err != nil {
		return err
	}
	sess.Header = *hdr
	return s.writeFullSession(sess)
}

func (s *Store) Load(sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadUnlocked(sessionID)
}

func (s *Store) loadUnlocked(sessionID string) (*Session, error) {
	path := s.sessionPath(sessionID)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("no existe la sesión %s: %w", sessionID, err)
	}
	defer f.Close()

	sess := &Session{Messages: []Message{}}
	scanner := bufio.NewScanner(f)
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var rec LineRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("%s:%d json corrupto: %w", path, lineNo, err)
		}

		switch rec.Type {
		case RecordHeader:
			if rec.Header != nil {
				sess.Header = *rec.Header
			}
		case RecordMessage:
			if rec.Message != nil {
				sess.Messages = append(sess.Messages, *rec.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error leyendo sesión %s: %w", path, err)
	}

	if sess.ID == "" {
		return nil, fmt.Errorf("sesión %s sin cabecera válida", sessionID)
	}

	return sess, nil
}

func (s *Store) List() ([]*Header, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Header{}, nil
		}
		return nil, fmt.Errorf("error listando sesiones en %s: %w", s.dir, err)
	}

	var headers []*Header
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		sess, err := s.loadUnlocked(id)
		if err != nil {
			continue
		}
		h := sess.Header
		headers = append(headers, &h)
	}

	sort.Slice(headers, func(i, j int) bool {
		return headers[i].UpdatedAt.After(headers[j].UpdatedAt)
	})

	return headers, nil
}

func (s *Store) GetLatest() (*Session, error) {
	headers, err := s.List()
	if err != nil || len(headers) == 0 {
		return nil, fmt.Errorf("no hay sesiones guardadas")
	}
	return s.Load(headers[0].ID)
}

func (s *Store) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.sessionPath(sessionID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error eliminando sesión %s: %w", sessionID, err)
	}
	return nil
}
