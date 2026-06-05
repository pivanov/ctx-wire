package install

import "fmt"

func ensureJSONObject(parent map[string]any, key, path string) (map[string]any, error) {
	v, ok := parent[key]
	if !ok {
		m := map[string]any{}
		parent[key] = m
		return m, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parse %s: %q must be an object", path, key)
	}
	return m, nil
}

func optionalJSONArray(parent map[string]any, key, path string) ([]any, error) {
	v, ok := parent[key]
	if !ok {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("parse %s: %q must be an array", path, key)
	}
	return a, nil
}
