package catalog

import (
	"fmt"
	"strings"
)

// Interest identifies either a category or an individual interest
// item. Categories and items share the same numeric space.
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Interest uint16

// Interest values. Categories are listed first, followed by their
// items.
const (
	// InterestTeamSports is the team sports category.
	InterestTeamSports Interest = iota + 1
	InterestBasketball
	InterestSoccer
	InterestBaseball
	InterestHockey
	InterestVolleyball

	// InterestIndividualSports is the individual sports category.
	InterestIndividualSports
	InterestTennis
	InterestSwimming
	InterestTrack
	InterestGolf
	InterestCycling
)

var interestToString = map[Interest]string{
	InterestTeamSports:       "team-sports",
	InterestBasketball:       "basketball",
	InterestSoccer:           "soccer",
	InterestBaseball:         "baseball",
	InterestHockey:           "hockey",
	InterestVolleyball:       "volleyball",
	InterestIndividualSports: "individual-sports",
	InterestTennis:           "tennis",
	InterestSwimming:         "swimming",
	InterestTrack:            "track",
	InterestGolf:             "golf",
	InterestCycling:          "cycling",
}

var interestToLabel = map[Interest]string{
	InterestTeamSports:       "Team Sports",
	InterestBasketball:       "Basketball",
	InterestSoccer:           "Soccer",
	InterestBaseball:         "Baseball",
	InterestHockey:           "Hockey",
	InterestVolleyball:       "Volleyball",
	InterestIndividualSports: "Individual Sports",
	InterestTennis:           "Tennis",
	InterestSwimming:         "Swimming",
	InterestTrack:            "Track",
	InterestGolf:             "Golf",
	InterestCycling:          "Cycling",
}

var interestLookup = func() map[string]Interest {
	lookup := make(map[string]Interest, len(interestToString))
	for k, v := range interestToString {
		lookup[v] = k
	}
	return lookup
}()

func (i Interest) String() string {
	if str, ok := interestToString[i]; ok {
		return str
	}
	return fmt.Sprintf("Interest(%d)", i)
}

// Label returns the human-readable label for the interest.
func (i Interest) Label() string {
	if label, ok := interestToLabel[i]; ok {
		return label
	}
	return i.String()
}

// MarshalText implements encoding.TextMarshaler.
func (i Interest) MarshalText() ([]byte, error) {
	if _, ok := interestToString[i]; !ok {
		return nil, fmt.Errorf("invalid interest: %d", i)
	}
	return []byte(i.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *Interest) UnmarshalText(text []byte) error {
	parsed, ok := interestLookup[strings.ToLower(string(text))]
	if !ok {
		return fmt.Errorf("unknown interest: %q", text)
	}
	*i = parsed
	return nil
}

// InterestGroup is a category with its member items.
type InterestGroup struct {
	Category Interest
	Items    []Interest
}

// groups is the static catalog of interest groups.
var groups = []InterestGroup{
	{
		Category: InterestTeamSports,
		Items: []Interest{
			InterestBasketball,
			InterestSoccer,
			InterestBaseball,
			InterestHockey,
			InterestVolleyball,
		},
	},
	{
		Category: InterestIndividualSports,
		Items: []Interest{
			InterestTennis,
			InterestSwimming,
			InterestTrack,
			InterestGolf,
			InterestCycling,
		},
	},
}

// categoryIndex maps each item to its parent category.
var categoryIndex = func() map[Interest]Interest {
	idx := make(map[Interest]Interest)
	for _, group := range groups {
		for _, item := range group.Items {
			idx[item] = group.Category
		}
	}
	return idx
}()

// itemIndex maps each category to its items.
var itemIndex = func() map[Interest][]Interest {
	idx := make(map[Interest][]Interest, len(groups))
	for _, group := range groups {
		idx[group.Category] = group.Items
	}
	return idx
}()

// Groups returns the interest catalog.
func Groups() []InterestGroup {
	return groups
}

// CategoryOf returns the parent category for an item. Returns
// false if the interest is itself a category or is unknown.
func CategoryOf(item Interest) (Interest, bool) {
	cat, ok := categoryIndex[item]
	return cat, ok
}

// ItemsIn returns the items belonging to a category. Returns nil
// if the interest is not a category.
func ItemsIn(category Interest) []Interest {
	return itemIndex[category]
}

// IsCategory reports whether the interest is a category (has
// child items).
func IsCategory(i Interest) bool {
	_, ok := itemIndex[i]
	return ok
}
