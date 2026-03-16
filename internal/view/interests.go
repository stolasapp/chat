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

// interestGroup is a view-layer interest group with string values
// for template rendering.
type interestGroup struct {
	Name  string
	Items []interestItem
}

type interestItem struct {
	Value string
	Label string
}

// interestGroupOpts is computed once from the static catalog.
var interestGroupOpts = func() []interestGroup {
	catalogGroups := catalog.Groups()
	viewGroups := make([]interestGroup, len(catalogGroups))
	for idx, group := range catalogGroups {
		items := make([]interestItem, len(group.Items))
		for jdx, item := range group.Items {
			items[jdx] = interestItem{Value: item.String(), Label: item.Label()}
		}
		viewGroups[idx] = interestGroup{
			Name:  group.Category.Label(),
			Items: items,
		}
	}
	return viewGroups
}()
