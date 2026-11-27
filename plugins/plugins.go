// Package plugins is the composition list of first-party integrations.
package plugins

import "github.com/vanvanni/plonkr/plugin"

// All currently returns an empty set: provider implementations ship separately.
func All() plugin.Set {
	s, err := plugin.NewSet()
	if err != nil {
		panic(err)
	}
	return s
}
