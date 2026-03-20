package view

import (
	"cmp"
	"slices"

	"github.com/stolasapp/chat/internal/catalog"
	"github.com/stolasapp/chat/internal/match"
)

// NewMatchedProfile builds a MatchedProfile from the partner's
// profile and the user's own interests. Common interests appear
// first, sorted by label; other interests follow, also sorted.
func NewMatchedProfile(partner *match.Profile, myInterests catalog.Set[catalog.Interest]) MatchedProfile {
	var common, other []catalog.Interest
	for interest := range partner.Interests {
		if myInterests.Contains(interest) {
			common = append(common, interest)
		} else {
			other = append(other, interest)
		}
	}

	byLabel := func(a, b catalog.Interest) int {
		return cmp.Compare(a.Label(), b.Label())
	}
	slices.SortFunc(common, byLabel)
	slices.SortFunc(other, byLabel)

	profile := MatchedProfile{
		Gender: partner.Gender.Label(),
		Role:   partner.Role.Label(),
		Common: common,
		Other:  other,
	}
	if partner.Species != "" && partner.Species != catalog.SpeciesOther {
		profile.Species = partner.Species.Label()
	}
	return profile
}
