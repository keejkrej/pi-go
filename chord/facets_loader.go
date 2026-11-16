// Ported from packages/chord/src/facets/loader.ts (pi v1.0.0).

package chord

func DisposeLoadedFacets(loaded []*LoadedFacets) ([]any, error) {
	if len(loaded) == 0 {
		return nil, nil
	}
	var errs []any
	for _, entry := range loaded {
		if entry == nil || entry.Dispose == nil {
			continue
		}
		if err := entry.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs, nil
}
