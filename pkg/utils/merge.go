package utils

import "slices"

// MergeMaps merges m2 key/values into m1 without overriding existing keys
func MergeMaps[Map ~map[K]V, K comparable, V any](m1, m2 Map, skips ...K) Map {
	if m1 == nil {
		m1 = map[K]V{}
	}
	for k, v := range m2 {
		if slices.Contains(skips, k) {
			continue
		}
		if _, ok := m1[k]; !ok {
			m1[k] = v
		}
	}
	return m1
}
