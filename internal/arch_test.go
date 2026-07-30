package internal_test

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestTUINoImportaHTTP(t *testing.T) {
	out, _ := exec.Command("go", "list", "-deps", "./internal/tui").Output()
	if bytes.Contains(out, []byte("net/http")) {
		t.Fatal("internal/tui importa net/http: la frontera arquitectónica está rota")
	}
}
