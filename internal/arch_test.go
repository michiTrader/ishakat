package internal_test

import (
	"bytes"
	"os"
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
//
// Sobre los caminos: se usa la ruta completa del módulo y no "./internal/tui".
// Un test corre con el directorio de trabajo puesto en el del paquete, o sea
// internal/, donde "./internal/tui" apunta a internal/internal/tui y no existe.
// Eso ya pasó una vez: go list fallaba, el error se leía como "no hay
// toolchain", y los cuatro tests de frontera llevaban meses saltándose en verde
// sin comprobar nada. Un guardia que se salta solo es peor que no tener guardia,
// porque además da confianza.
const mod = "github.com/MichiTrader/ishakat/"

// deps devuelve la lista de dependencias transitivas de un paquete. Solo salta
// el test si de verdad no hay toolchain; si go list existe y falla, es un fallo,
// porque significa que la pregunta no se pudo hacer.
func deps(t *testing.T, pkg string) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go no está en PATH: el test de frontera necesita el toolchain")
	}
	var stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-deps", mod+pkg)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s falló (%v): %s", mod+pkg, err, stderr.String())
	}
	if len(out) == 0 {
		t.Fatalf("go list -deps %s no devolvió nada", mod+pkg)
	}
	return out
}

func TestTUINoImportaHTTP(t *testing.T) {
	if bytes.Contains(deps(t, "internal/tui"), []byte("net/http")) {
		t.Fatal("internal/tui importa net/http: la frontera arquitectónica está rota")
	}
}

// TestProviderNoImportaPresentacion es el simétrico prometido en §6.1: el
// adaptador no puede saber nada de colores, estilos ni del bucle de la
// interfaz. Si esto falla, la traducción del historial y la presentación
// quedaron pegadas — la misma regla que ya cubría a openai cubre ahora
// también a anthropic (Fase 4).
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

	for _, pkg := range []string{"internal/provider", "internal/provider/openai", "internal/provider/anthropic", "internal/provider/gemini"} {
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
	list := string(deps(t, "internal/convo"))
	for _, mal := range []string{"net/http", "lipgloss", "bubbletea", "ishakat/internal/provider"} {
		if strings.Contains(list, mal) {
			t.Errorf("internal/convo importa %s: los tipos del contrato 1 tienen que ser puros", mal)
		}
	}
}

// depsOpt es como deps pero salta el test si el paquete todavía no existe. Los
// límites de la Fase 2.5 se escriben antes que el código que deben limitar: una
// regla añadida después de que el acoplamiento ya ocurrió llega tarde, porque a
// esas alturas quitarlo es refactorizar y no corregir. El test queda dormido y
// se despierta solo con el primer archivo del paquete.
func depsOpt(t *testing.T, pkg, dir string) (string, bool) {
	t.Helper()
	// dir es relativo al directorio del test (internal/), que es donde corre.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skipf("internal/%s todavía no existe; la regla se activará con el primer archivo", dir)
		return "", false
	}
	return string(deps(t, pkg)), true
}

// TestEngineNoImportaProvider fija la frontera que el paso 14 va a tener más
// tentación de romper. El bucle agéntico necesita el tipo de una llamada a
// herramienta, y el tipo ya existe en internal/provider — importarlo desde ahí
// es una línea. Pero provider importa net/http, y §6.1 prohíbe HTTP de forma
// transitiva desde el TUI; en el momento en que engine importe provider, el TUI
// lo hereda y TestTUINoImportaHTTP se cae sin que nadie entienda por qué.
//
// De ahí la duplicación deliberada de §12bis: engine define su propio
// EventToolCall y recibe un ToolRunner como función. Duplicar una struct
// pequeña es más barato que fusionar dos capas.
func TestEngineNoImportaProvider(t *testing.T) {
	list := string(deps(t, "internal/engine"))
	for _, mal := range []string{"net/http", "ishakat/internal/provider", "ishakat/internal/tools", "lipgloss", "bubbletea"} {
		if strings.Contains(list, mal) {
			t.Errorf("internal/engine importa %s: el bucle no sabe de transporte ni de presentación.\n"+
				"  Si necesitas un tipo de provider o de tools, defínelo en engine o pásalo como función (§12bis)", mal)
		}
	}
}

// TestToolsNoImportaTUI protege la propiedad que hace posible la tercera puerta
// (§1): las herramientas tienen que ejecutarse igual desde el TUI, desde -p y
// desde serve. En cuanto una herramienta pueda pedirle algo a la interfaz, deja
// de funcionar sin ella, y el modo headless empieza a fallar en los casos que
// nadie prueba porque solo aparecen sin terminal.
//
// Los permisos de §19.6 son el caso límite: sí necesitan preguntarle a un
// humano. Por eso se preguntan a través de una interfaz que el llamador
// implementa (y que sin TTY responde "no"), no llamando al TUI.
func TestToolsNoImportaTUI(t *testing.T) {
	list, ok := depsOpt(t, "internal/tools", "tools")
	if !ok {
		return
	}
	for _, mal := range []string{"ishakat/internal/tui", "lipgloss", "bubbletea", "bubbles"} {
		if strings.Contains(list, mal) {
			t.Errorf("internal/tools importa %s: una herramienta que necesita interfaz no funciona en -p ni en serve.\n"+
				"  Los permisos se piden por una interfaz que implementa el llamador (§19.6), no llamando al TUI", mal)
		}
	}
}

// TestAskStaysPresentationFree protects §21.7's own reason for existing:
// internal/ask is "the primitive", shared unchanged by the TUI, serve, and
// (not asking at all) headless. If it imported any presentation package or
// a concrete door's own transport, only one door could use it and the
// whole point of collecting the duplicated round-trip logic in one place
// (docs/PLAN.md §21.7: "which deletes the duplicated round-trip logic
// currently in internal/app/toolreview.go and internal/app/serve.go")
// would be lost — a form that hard-depends on Bubble Tea cannot also
// answer a WebSocket permission_request.
func TestAskStaysPresentationFree(t *testing.T) {
	list, ok := depsOpt(t, "internal/ask", "ask")
	if !ok {
		return
	}
	for _, mal := range []string{
		"net/http", "lipgloss", "bubbletea", "bubbles", "colorprofile",
		"ishakat/internal/tui", "ishakat/internal/theme", "ishakat/internal/config",
		"ishakat/internal/permissions",
	} {
		if strings.Contains(list, mal) {
			t.Errorf("internal/ask imports %s: the primitive must stay usable from every door (TUI, serve, headless) unchanged", mal)
		}
	}
}
