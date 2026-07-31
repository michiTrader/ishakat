package provider

import (
	"fmt"
	"sort"
	"sync"
)

// El registro resuelve un problema de dependencias, no de elegancia: si este
// paquete importara provider/openai para construirlo, y openai importa este
// paquete para implementar la interfaz, habría un ciclo. La solución es la
// misma de database/sql: cada dialecto se registra a sí mismo en su init() y
// internal/app lo activa con un import en blanco.
//
//	import _ "github.com/MichiTrader/ishakat/internal/provider/openai"
//
// Agregar un proveedor nuevo del dialecto OpenAI no requiere ni eso: son
// cinco líneas de TOML, porque kind = "openai" ya está registrado (§5.2).

// Constructor arma un adaptador a partir de su configuración.
type Constructor func(Settings) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Constructor{}
)

// Register asocia un kind con su constructor. Se llama desde el init() del
// subpaquete del dialecto. Registrar dos veces el mismo kind es un error de
// programación, no de usuario, y por eso entra en panic al arrancar en vez de
// fallar silenciosamente más tarde.
func Register(kind string, c Constructor) {
	if kind == "" {
		panic("provider.Register: kind vacío")
	}
	if c == nil {
		panic("provider.Register: constructor nil para kind " + kind)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[kind]; dup {
		panic("provider.Register: kind duplicado " + kind)
	}
	registry[kind] = c
}

// New construye el adaptador que corresponde a s.Kind.
func New(s Settings) (Provider, error) {
	if s.ID == "" {
		return nil, fmt.Errorf("provider: falta el id del proveedor")
	}
	kind := s.Kind
	if kind == "" {
		kind = "openai" // el dialecto por defecto de §5.2
	}

	registryMu.RLock()
	c, ok := registry[kind]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (registrados: %v)", ErrUnknownKind, kind, Kinds())
	}
	return c(s)
}

// Kinds lista los dialectos registrados, ordenados. Sirve para el mensaje de
// error de arriba y para `ishakat doctor`.
func Kinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Registered indica si un kind tiene adaptador. La validación de la
// configuración lo usa para avisar en la carga en vez de al primer turno.
func Registered(kind string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[kind]
	return ok
}
