package view

import "github.com/stolasapp/chat/internal/catalog"

// selectOption is a value/label pair for select elements.
type selectOption struct {
	Value string
	Label string
}

// genderOpts is computed once from the static catalog.
var genderOpts = func() []selectOption {
	genders := catalog.Genders()
	opts := make([]selectOption, len(genders))
	for idx, gender := range genders {
		opts[idx] = selectOption{Value: gender.String(), Label: gender.Label()}
	}
	return opts
}()

// roleOpts is computed once from the static catalog.
var roleOpts = func() []selectOption {
	roles := catalog.Roles()
	opts := make([]selectOption, len(roles))
	for idx, role := range roles {
		opts[idx] = selectOption{Value: role.String(), Label: role.Label()}
	}
	return opts
}()

// speciesOpts is computed once from the static catalog.
var speciesOpts = func() []selectOption {
	species := catalog.AllSpecies()
	opts := make([]selectOption, len(species))
	for idx, s := range species {
		opts[idx] = selectOption{Value: s.String(), Label: s.Label()}
	}
	return opts
}()

// interestOpts is computed once from the static catalog as a flat
// list (no grouping).
var interestOpts = func() []selectOption {
	interests := catalog.AllInterests()
	opts := make([]selectOption, len(interests))
	for idx, interest := range interests {
		opts[idx] = selectOption{Value: interest.String(), Label: interest.Label()}
	}
	return opts
}()
