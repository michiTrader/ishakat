package internal_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// La frontera de §6.1 se prueba, no se promete: "el TUI no sabe qué es HTTP y
// el proveedor no sabe qué es un color". Son veinte líneas que evitan el
// acoplamiento que después hace imposible testear.
//
// go list -deps devuelve el cierre transitivo, así que estos tests detectan
// también las dependencias indirectas, que son las que se cuelan sin que nadie
// se dé cuenta.

// deps devuelve la lista de dependencias transitivas de un paquete.
func deps(t *testing.T, pkg string) []byte {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Skipf("no se pudo ejecutar go list (%v): el test necesita el toolchain en PATH", err)
	}
	if len(out) == 0 {
		t.Fatalf("go list -deps %s no devolvió nada", pkg)
	}
	return out
}

func TestTUINoImportaHTTP(t *testing.T) {
	if bytes.Contains(deps(t, "./internal/tui"), []byte("net/http")) {
		t.Fatal("internal/tui importa net/http: la frontera arquitectónica está rota")
	}
}

// TestProviderNoImportaPresentacion es el simétrico prometido en §6.1: el
// adaptador no puede saber nada de colores, estilos ni del bucle de la
// interfaz. Si esto falla, la traducción del historial y la presentación
// quedaron pegadas y el siguiente dialecto (Anthropic, Fase 4) hereda el
// problema.
func TestProviderNoImportaPresentacion(t *testing.T) {
	prohibidos := []string{
		"lipgloss",
		"bubbletea",
		"bubbles",
		"colorprofile",
		"ishakat/internal/tui",
		"ishakat/internal/theme",
		"ishakat/internal/config",
	}

	for _, pkg := range []string{"./internal/provider", "./internal/provider/openai"} {
		list := string(deps(t, pkg))
		for _, mal := range prohibidos {
			if strings.Contains(list, mal) {
				t.Errorf("%s importa %s: el proveedor no sabe qué es un color ni qué es la configuración", pkg, mal)
			}
		}
	}
}

// TestConvoEsPuro protege el contrato 1 (§4): el modelo de conversación es lo
// único que cruza todas las fronteras, y solo puede hacerlo si no arrastra
// nada consigo. Si convo empezara a depender de HTTP o de la interfaz, dejaría
// de servir como moneda común.
func TestConvoEsPuro(t *testing.T) {
	list := string(deps(t, "./internal/convo"))
	for _, mal := range []string{"net/http", "lipgloss", "bubbletea", "ishakat/internal/provider"} {
		if strings.Contains(list, mal) {
			t.Errorf("internal/convo importa %s: los tipos del contrato 1 tienen que ser puros", mal)
		}
	}
}
