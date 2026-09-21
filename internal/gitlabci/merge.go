package gitlabci

// mergeConfig merges src into dst. src wins on conflicts.
// Maps are deep-merged; arrays and scalars are replaced.
func mergeConfig(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok {
			dst[k] = clone(sv)
			continue
		}
		dm, dIsMap := asMap(dv)
		sm, sIsMap := asMap(sv)
		if dIsMap && sIsMap {
			dst[k] = mergeConfig(dm, sm)
			continue
		}
		dst[k] = clone(sv)
	}
	return dst
}
