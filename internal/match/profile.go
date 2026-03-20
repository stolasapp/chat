// Package match implements matchmaking: user profiles, compatibility
// checks, interest scoring, and the queue-based matcher.
package match

import (
	"github.com/stolasapp/chat/internal/catalog"
)

// Profile holds a user's profile, filters, and block list
// for matchmaking.
type Profile struct {
	Gender           catalog.Gender                `json:"gender"`
	Role             catalog.Role                  `json:"role"`
	Species          catalog.Species               `json:"species"`
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

	// exclude interests: exact match only
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
// the partner's interests.
func excludeHit(excludes, interests catalog.Set[catalog.Interest]) bool {
	for excl := range excludes {
		if interests.Contains(excl) {
			return true
		}
	}
	return false
}

const (
	// jaccardDirections is the number of scoring directions
	// (A->B and B->A) used to normalize the Jaccard.
	jaccardDirections = 2.0

	// wildcardScore is the score when either or both users have
	// no interests selected. Treated as "open to anyone" with a
	// small positive score so they match immediately without
	// waiting for the timeout fallback.
	wildcardScore = 0.01
)

// Score computes Jaccard similarity between two profiles'
// interests. If either side has no interests, a small positive
// wildcard score is returned so they still match.
func Score(profA, profB *Profile) float64 {
	if profA.Interests.Len() == 0 || profB.Interests.Len() == 0 {
		return wildcardScore
	}

	// compute match weight: 1.0 for each exact match
	weightSum := 0.0
	for interest := range profA.Interests {
		if profB.Interests.Contains(interest) {
			weightSum += 1.0
		}
	}
	for interest := range profB.Interests {
		if profA.Interests.Contains(interest) {
			weightSum += 1.0
		}
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

	return weightSum / (jaccardDirections * float64(unionSize))
}
