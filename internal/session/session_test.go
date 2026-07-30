package session_test

import (
	"os"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/session"
)

func TestSessionLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("error creando store: %v", err)
	}

	// 1. Crear sesión
	sess, err := store.NewSession("omniroute/auto/coding", "Mi Sesión de Prueba")
	if err != nil {
		t.Fatalf("error creando sesión: %v", err)
	}

	if sess.ID == "" || sess.Title != "Mi Sesión de Prueba" {
		t.Fatalf("cabecera de sesión inválida: %+v", sess.Header)
	}

	// 2. Hacer Append de mensajes
	msg1 := session.Message{
		ID:        "m1",
		Role:      session.RoleUser,
		Content:   "Hola, ¿cómo funciona Go?",
		Timestamp: time.Now(),
	}
	if err := store.AppendMessage(sess.ID, msg1); err != nil {
		t.Fatalf("error agregando mensaje 1: %v", err)
	}

	msg2 := session.Message{
		ID:        "m2",
		Role:      session.RoleAssistant,
		Content:   "Go es un lenguaje compilado y concurrente.",
		Reasoning: "Explicación directa",
		Timestamp: time.Now(),
		Usage: session.TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 12,
			TotalTokens:      22,
		},
	}
	if err := store.AppendMessage(sess.ID, msg2); err != nil {
		t.Fatalf("error agregando mensaje 2: %v", err)
	}

	// 3. Cargar sesión y verificar mensajes
	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("error cargando sesión: %v", err)
	}

	if len(loaded.Messages) != 2 {
		t.Fatalf("esperados 2 mensajes, obtenidos %d", len(loaded.Messages))
	}

	if loaded.Messages[0].Content != msg1.Content || loaded.Messages[1].Reasoning != "Explicación directa" {
		t.Errorf("contenido de mensaje no coincide: %+v", loaded.Messages)
	}

	// 4. Probar cambio de título y listado
	if err := store.UpdateTitle(sess.ID, "Título Actualizado"); err != nil {
		t.Fatalf("error actualizando título: %v", err)
	}

	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("error listando sesiones, devueltas: %d", len(list))
	}
	if list[0].Title != "Título Actualizado" {
		t.Errorf("esperado 'Título Actualizado', obtenido '%s'", list[0].Title)
	}

	// 5. Probar GetLatest
	latest, err := store.GetLatest()
	if err != nil || latest.ID != sess.ID {
		t.Fatalf("GetLatest devolvió sesión incorrecta: %v", err)
	}

	// 6. Eliminar sesión
	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("error eliminando sesión: %v", err)
	}

	if _, err := store.Load(sess.ID); err == nil {
		t.Fatal("se esperaba error al cargar una sesión eliminada")
	}
}

func TestCorruptedSessionHandling(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := session.NewStore(tmpDir)

	sess, _ := store.NewSession("model", "Test Corrupt")
	p := tmpDir + "/" + sess.ID + ".jsonl"

	// Escribir basura al final del archivo
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("\n{json-invalido-basura\n")
	_ = f.Close()

	if _, err := store.Load(sess.ID); err == nil {
		t.Fatal("se esperaba error al cargar sesión corrupta")
	}
}
