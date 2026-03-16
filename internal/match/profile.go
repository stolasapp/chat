// Package match implements matchmaking: user profiles, compatibility
// checks, interest scoring, and the queue-based matcher.
package match

import (
	"slices"

	"github.com/stolasapp/chat/internal/catalog"
)

// Profile holds a user's profile, filters, and block list
// for matchmaking.
type Profile struct {
	Gender           catalog.Gender                `json:"gender"`
	Role             catalog.Role                  `json:"role"`
	Interests        catalog.Set[catalog.Interest] `json:"interests"`
	FilterGender     catalog.Set[catalog.Gender]   `json:"filter_gender"`
	FilterRole       catalog.Set[catalog.Role]     `json:"filter_role"`
	ExcludeInterests catalog.Set[catalog.Interest] `json:"exclude_interests"`
	BlockedTokens    catalog.Set[Token]            `json:"-"`
}

// Compatible reports whether two profiles can be matched. Both
// filter directions are checked symmetrically. tokenA and tokenB
// are used for block list checks.
func Compatible(profA, profB *Profile, tokenA, tokenB Token) bool {
	// blocked check
	if profA.BlockedTokens.Contains(tokenB) {
		return false
	}
	if profB.BlockedTokens.Contains(tokenA) {
		return false
	}

	// gender filter: if A specifies gender filters, B's gender
	// must be in A's list
	if !genderFilterSatisfied(profA.FilterGender, profB.Gender) {
		return false
	}
	if !genderFilterSatisfied(profB.FilterGender, profA.Gender) {
		return false
	}

	// role filter: uses hierarchy
	if !roleFilterSatisfied(profA.FilterRole, profB.Role) {
		return false
	}
	if !roleFilterSatisfied(profB.FilterRole, profA.Role) {
		return false
	}

	// exclude interests
	if excludeHit(profA.ExcludeInterests, profB.Interests) {
		return false
	}
	if excludeHit(profB.ExcludeInterests, profA.Interests) {
		return false
	}

	return true
}

func genderFilterSatisfied(filters catalog.Set[catalog.Gender], gender catalog.Gender) bool {
	return filters.Len() == 0 || filters.Contains(gender)
}

func roleFilterSatisfied(filters catalog.Set[catalog.Role], role catalog.Role) bool {
	if filters.Len() == 0 {
		return true
	}
	for filter := range filters {
		if catalog.RoleMatchesFilter(role, filter) {
			return true
		}
	}
	return false
}

// excludeHit reports whether any excluded interest matches any of
// the partner's interests. Excluding a category blocks any item
// under it; excluding an item blocks only that exact item.
func excludeHit(excludes, interests catalog.Set[catalog.Interest]) bool {
	for excl := range excludes {
		if interests.Contains(excl) {
			return true
		}
		// excluding a category also blocks any item under it
		if catalog.IsCategory(excl) &&
			slices.ContainsFunc(catalog.ItemsIn(excl), interests.Contains) {
			return true
		}
	}
	return false
}

const (
	// crossLevelDiscount is applied to cross-level interest
	// matches (category-to-item or item-to-category).
	crossLevelDiscount = 0.75

	// jaccardDirections is the number of scoring directions
	// (A->B and B->A) used to normalize the weighted Jaccard.
	jaccardDirections = 2.0

	// wildcardScore is the score when either or both users have
	// no interests selected. Treated as "open to anyone" with a
	// small positive score so they match immediately without
	// waiting for the timeout fallback.
	wildcardScore = 0.01
)

// Score computes weighted Jaccard similarity between two profiles'
// interests. Exact matches (same interest) score 1.0. Cross-level
// matches (category matched to an item under it, or vice versa)
// score crossLevelDiscount. If either side has no interests, a
// small positive wildcard score is returned so they still match.
func Score(profA, profB *Profile) float64 {
	if profA.Interests.Len() == 0 || profB.Interests.Len() == 0 {
		return wildcardScore
	}

	// compute best match weight for each of A's interests in B
	weightSum := 0.0
	for interest := range profA.Interests {
		weightSum += bestMatch(interest, profB.Interests)
	}
	// and B's interests in A
	for interest := range profB.Interests {
		weightSum += bestMatch(interest, profA.Interests)
	}

	// union = |A| + |B| - |intersection|
	intersection := 0
	for interest := range profA.Interests {
		if profB.Interests.Contains(interest) {
			intersection++
		}
	}
	unionSize := profA.Interests.Len() + profB.Interests.Len() - intersection

	if unionSize == 0 {
		return 0
	}

	// each interest contributes a match from both directions;
	// normalize by 2 * union to keep score in [0, 1]
	return weightSum / (jaccardDirections * float64(unionSize))
}

// bestMatch finds the best match weight for interest in the
// partner's set.
func bestMatch(interest catalog.Interest, partnerSet catalog.Set[catalog.Interest]) float64 {
	// exact match
	if partnerSet.Contains(interest) {
		return 1.0
	}

	// cross-level: if interest is a category, check if partner
	// has any item under it
	if catalog.IsCategory(interest) &&
		slices.ContainsFunc(catalog.ItemsIn(interest), partnerSet.Contains) {
		return crossLevelDiscount
	}

	// cross-level: if interest is an item, check if partner has
	// its parent category
	if cat, ok := catalog.CategoryOf(interest); ok {
		if partnerSet.Contains(cat) {
			return crossLevelDiscount
		}
	}

	return 0
}
