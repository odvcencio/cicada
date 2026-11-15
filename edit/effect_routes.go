package edit

import "sort"

// EffectRoute is where one effect kind may sit, as addeffect and the language
// allow it: the chains it may be inserted on (track, the music bus, master),
// whether it is a send return, and whether it works only in an insert chain.
type EffectRoute struct {
	Kind        string   `json:"kind"`
	InsertOn    []string `json:"insert_on"`
	Return      bool     `json:"return"`
	InsertsOnly bool     `json:"inserts_only"`
}

// EffectRoutes lists every effect kind addeffect accepts with its routes,
// sorted by kind. It reads the tables addeffect itself checks.
func EffectRoutes() []EffectRoute {
	routes := make([]EffectRoute, 0, len(mixerEffectKinds))
	for _, kind := range mixerEffectKinds {
		routes = append(routes, EffectRoute{Kind: kind, InsertOn: append([]string{}, effectInsertRoutes[kind]...), Return: effectIsReturn[kind], InsertsOnly: effectNeedsInsert[kind]})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Kind < routes[j].Kind })
	return routes
}
