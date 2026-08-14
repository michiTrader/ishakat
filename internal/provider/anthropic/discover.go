package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// maxModelList acota la respuesta de GET /v1/models. El catálogo real de
// Anthropic es un puñado de modelos (decenas, no cientos); dos megabytes
// dejan el mismo margen de sobra que el dialecto OpenAI usa para su propio
// catálogo, mucho más grande.
const maxModelList = 2 << 20

// Discover lista los modelos que el servicio declara en GET /v1/models.
//
// Devuelve los datos casi crudos, igual que openai.Discover: la
// normalización y la fusión con models.dev son trabajo del catálogo
// (§4.3, Paso 6), no de este adaptador. A diferencia del dialecto OpenAI, la
// Messages API no reporta ventana de contexto ni límite de salida en esta
// lista —Context y Output quedan en cero, y el catálogo los completa desde
// models.dev— y solo lee la primera página (ver el comentario de
// wireModelList en wire.go).
func (p *Provider) Discover(ctx context.Context) ([]provider.RawModel, error) {
	req, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, p.netError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.httpError(resp)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelList))
	if err != nil {
		return nil, fmt.Errorf("anthropic: error leyendo el catálogo de %s: %w", p.set.ID, err)
	}

	var list wireModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("anthropic: catálogo ilegible de %s: %w", p.set.ID, err)
	}

	out := make([]provider.RawModel, 0, len(list.Data))
	for _, m := range list.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		entry, err := json.Marshal(m)
		if err != nil {
			continue // una entrada rota no invalida el catálogo entero
		}
		out = append(out, provider.RawModel{
			WireID: m.ID,
			Name:   name,
			Raw:    entry,
		})
	}
	return out, nil
}
