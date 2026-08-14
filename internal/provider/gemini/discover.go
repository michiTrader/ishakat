package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// maxModelList acota la respuesta de GET /v1beta/models. El catálogo de
// Gemini es más grande que el de Anthropic pero no se acerca al de OpenAI;
// el mismo margen de dos megabytes que anthropic.Discover usa alcanza de
// sobra para la primera página (ver el comentario de wireModelList en
// wire.go sobre por qué solo se lee una página).
const maxModelList = 2 << 20

// Discover lista los modelos que el servicio declara en GET /v1beta/models.
//
// A diferencia del dialecto de Anthropic (que deja Context/Output en cero
// porque su propio listado no los reporta), aquí sí se rellenan desde
// inputTokenLimit/outputTokenLimit — el Model resource de Gemini los trae
// directos, el mismo patrón que openai.Discover ya sigue para su propio
// catálogo. Se descartan las entradas cuyo supportedGenerationMethods no
// incluya "generateContent": el catálogo de Gemini también lista modelos de
// solo-embeddings (embedding-001, etc.) que este adaptador no puede usar.
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
		return nil, fmt.Errorf("gemini: error leyendo el catálogo de %s: %w", p.set.ID, err)
	}

	var list wireModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("gemini: catálogo ilegible de %s: %w", p.set.ID, err)
	}

	out := make([]provider.RawModel, 0, len(list.Models))
	for _, m := range list.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if strings.TrimSpace(id) == "" {
			continue
		}
		if !supportsGenerateContent(m.SupportedGenerationMethods) {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		entry, err := json.Marshal(m)
		if err != nil {
			continue // una entrada rota no invalida el catálogo entero
		}
		out = append(out, provider.RawModel{
			WireID:  id,
			Name:    name,
			Context: m.InputTokenLimit,
			Output:  m.OutputTokenLimit,
			Raw:     entry,
		})
	}
	return out, nil
}

func supportsGenerateContent(methods []string) bool {
	for _, m := range methods {
		if m == "generateContent" {
			return true
		}
	}
	return false
}
