package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// maxModelList acota la respuesta de GET /models. El catálogo real de
// OmniRoute ronda los 300 KB; dos megabytes dejan margen de sobra y protegen
// de un servicio que devuelva basura infinita.
const maxModelList = 2 << 20

// Discover lista los modelos que el servicio declara en GET /models.
//
// Devuelve los datos casi crudos —incluido el JSON original de cada entrada—
// porque la normalización, el filtrado de deprecados y la fusión con
// models.dev son trabajo del catálogo (§4.3, Paso 6). Este adaptador solo
// traduce el sobre.
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
		return nil, fmt.Errorf("openai: error leyendo el catálogo de %s: %w", p.set.ID, err)
	}

	var list wireModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("openai: catálogo ilegible de %s: %w", p.set.ID, err)
	}
	if list.Error != nil && list.Error.Message != "" {
		return nil, &provider.Error{
			Provider: p.set.ID,
			Status:   resp.StatusCode,
			Code:     codeString(list.Error),
			Message:  list.Error.Message,
		}
	}

	out := make([]provider.RawModel, 0, len(list.Data))
	for _, entry := range list.Data {
		var m wireModel
		if err := json.Unmarshal(entry, &m); err != nil {
			continue // una entrada rota no invalida el catálogo entero
		}
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, provider.RawModel{
			WireID:  m.ID,
			Name:    name,
			Context: m.context(),
			Output:  m.output(),
			Tags:    m.Tags,
			Raw:     append(json.RawMessage(nil), entry...),
		})
	}
	return out, nil
}
