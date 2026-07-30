package config

func mergeRoot(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if k == "provider" {
			dst[k] = mergeProviders(dst[k], v)
			continue
		}
		dst[k] = mergeValue(dst[k], v)
	}
	return dst
}

func mergeValue(dst, src any) any {
	sm, sok := src.(map[string]any)
	dm, dok := dst.(map[string]any)
	if sok && dok {
		for k, v := range sm {
			dm[k] = mergeValue(dm[k], v)
		}
		return dm
	}
	return src
}

var providerTemplate = map[string]any{
	"kind":     "openai",
	"discover": true,
	"enabled":  true,
	"api_key":  "",
}

func mergeProviders(dstAny, srcAny any) any {
	dst, src := toTables(dstAny), toTables(srcAny)
	out := make([]map[string]any, 0, len(dst)+len(src))
	idx := map[string]int{}
	for _, p := range dst {
		id, _ := p["id"].(string)
		if id != "" {
			idx[id] = len(out)
		}
		out = append(out, p)
	}

	seenInSrc := map[string]bool{}
	for _, p := range src {
		id, _ := p["id"].(string)
		if id != "" && seenInSrc[id] {
			// ID duplicado en el mismo archivo/capa: se conserva para que Validate() lo detecte
			out = append(out, mergeRoot(cloneMap(providerTemplate), p))
			continue
		}
		if id != "" {
			seenInSrc[id] = true
		}
		if i, ok := idx[id]; ok && id != "" {
			out[i] = mergeRoot(out[i], p)
			continue
		}
		if id != "" {
			idx[id] = len(out)
		}
		out = append(out, mergeRoot(cloneMap(providerTemplate), p))
	}
	return out
}

func toTables(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
