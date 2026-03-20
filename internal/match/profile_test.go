package match

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/catalog"
)

func TestGender_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, gender := range catalog.Genders() {
		data, err := json.Marshal(gender)
		require.NoError(t, err)

		var got catalog.Gender
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, gender, got)
	}
}

func TestRole_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, role := range catalog.Roles() {
		data, err := json.Marshal(role)
		require.NoError(t, err)

		var got catalog.Role
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, role, got)
	}
}

func TestInterest_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, interest := range catalog.AllInterests() {
		data, err := json.Marshal(interest)
		require.NoError(t, err)

		var got catalog.Interest
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, interest, got)
	}
}

func TestSpecies_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, species := range catalog.AllSpecies() {
		data, err := json.Marshal(species)
		require.NoError(t, err)

		var got catalog.Species
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, species, got)
	}
}

func TestGender_UnmarshalText_Invalid(t *testing.T) {
	t.Parallel()

	var gender catalog.Gender
	assert.Error(t, gender.UnmarshalText([]byte("unknown")))
}

func TestRole_UnmarshalText_Invalid(t *testing.T) {
	t.Parallel()

	var role catalog.Role
	assert.Error(t, role.UnmarshalText([]byte("unknown")))
}

func TestInterest_UnmarshalText_Invalid(t *testing.T) {
	t.Parallel()

	var interest catalog.Interest
	assert.Error(t, interest.UnmarshalText([]byte("unknown")))
}

func TestSpecies_UnmarshalText_Invalid(t *testing.T) {
	t.Parallel()

	var species catalog.Species
	require.NoError(t, species.UnmarshalText([]byte("unknown")))
	assert.Equal(t, catalog.Species(""), species)
}

func TestRoleMatchesFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		role   catalog.Role
		filter catalog.Role
		want   bool
	}{
		{"dominant matches dominant", catalog.RoleDominant, catalog.RoleDominant, true},
		{"submissive matches submissive", catalog.RoleSubmissive, catalog.RoleSubmissive, true},
		{"switch matches switch", catalog.RoleSwitch, catalog.RoleSwitch, true},
		{"switch matches dominant filter", catalog.RoleSwitch, catalog.RoleDominant, true},
		{"switch matches submissive filter", catalog.RoleSwitch, catalog.RoleSubmissive, true},
		{"dominant does not match submissive", catalog.RoleDominant, catalog.RoleSubmissive, false},
		{"submissive does not match dominant", catalog.RoleSubmissive, catalog.RoleDominant, false},
		{"dominant does not match switch", catalog.RoleDominant, catalog.RoleSwitch, false},
		{"submissive does not match switch", catalog.RoleSubmissive, catalog.RoleSwitch, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, catalog.RoleMatchesFilter(test.role, test.filter))
		})
	}
}

func TestCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b *Profile
		want bool
	}{
		{
			name: "no filters, compatible",
			a:    &Profile{Gender: catalog.GenderMale, Role: catalog.RoleDominant},
			b:    &Profile{Gender: catalog.GenderFemale, Role: catalog.RoleSubmissive},
			want: true,
		},
		{
			name: "gender filter satisfied",
			a: &Profile{
				Gender:       catalog.GenderMale,
				Role:         catalog.RoleDominant,
				FilterGender: catalog.NewSet(catalog.GenderFemale),
			},
			b:    &Profile{Gender: catalog.GenderFemale, Role: catalog.RoleSubmissive},
			want: true,
		},
		{
			name: "gender filter not satisfied",
			a: &Profile{
				Gender:       catalog.GenderMale,
				Role:         catalog.RoleDominant,
				FilterGender: catalog.NewSet(catalog.GenderFemale),
			},
			b:    &Profile{Gender: catalog.GenderMale, Role: catalog.RoleSubmissive},
			want: false,
		},
		{
			name: "gender filter symmetric failure",
			a:    &Profile{Gender: catalog.GenderMale, Role: catalog.RoleDominant},
			b: &Profile{
				Gender:       catalog.GenderFemale,
				Role:         catalog.RoleSubmissive,
				FilterGender: catalog.NewSet(catalog.GenderFemale),
			},
			want: false,
		},
		{
			name: "role filter with hierarchy: switch matches dominant filter",
			a: &Profile{
				Gender:     catalog.GenderMale,
				Role:       catalog.RoleDominant,
				FilterRole: catalog.NewSet(catalog.RoleDominant),
			},
			b:    &Profile{Gender: catalog.GenderFemale, Role: catalog.RoleSwitch},
			want: true,
		},
		{
			name: "role filter: dominant does not match switch filter",
			a: &Profile{
				Gender:     catalog.GenderMale,
				Role:       catalog.RoleDominant,
				FilterRole: catalog.NewSet(catalog.RoleSwitch),
			},
			b:    &Profile{Gender: catalog.GenderFemale, Role: catalog.RoleDominant},
			want: false,
		},
		{
			name: "blocked A blocks B",
			a: &Profile{
				Gender:        catalog.GenderMale,
				Role:          catalog.RoleDominant,
				BlockedTokens: catalog.NewSet[Token]("tok-b"),
			},
			b:    &Profile{Gender: catalog.GenderFemale, Role: catalog.RoleSubmissive},
			want: false,
		},
		{
			name: "blocked B blocks A",
			a:    &Profile{Gender: catalog.GenderMale, Role: catalog.RoleDominant},
			b: &Profile{
				Gender:        catalog.GenderFemale,
				Role:          catalog.RoleSubmissive,
				BlockedTokens: catalog.NewSet[Token]("tok-a"),
			},
			want: false,
		},
		{
			name: "exclude exact interest",
			a: &Profile{
				Gender:           catalog.GenderMale,
				Role:             catalog.RoleDominant,
				ExcludeInterests: catalog.NewSet(catalog.Interest("Basketball")),
			},
			b: &Profile{
				Gender:    catalog.GenderFemale,
				Role:      catalog.RoleSubmissive,
				Interests: catalog.NewSet(catalog.Interest("Basketball")),
			},
			want: false,
		},
		{
			name: "exclude item does not block sibling",
			a: &Profile{
				Gender:           catalog.GenderMale,
				Role:             catalog.RoleDominant,
				ExcludeInterests: catalog.NewSet(catalog.Interest("Basketball")),
			},
			b: &Profile{
				Gender:    catalog.GenderFemale,
				Role:      catalog.RoleSubmissive,
				Interests: catalog.NewSet(catalog.Interest("Soccer")),
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := Compatible(test.a, test.b, "tok-a", "tok-b")
			assert.Equal(t, test.want, got)
			// symmetry: check reverse
			gotReverse := Compatible(test.b, test.a, "tok-b", "tok-a")
			assert.Equal(t, test.want, gotReverse, "symmetry check failed")
		})
	}
}

func TestScore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b *Profile
		want float64
	}{
		{
			name: "both empty",
			a:    &Profile{},
			b:    &Profile{},
			want: wildcardScore,
		},
		{
			name: "exact match single",
			a:    &Profile{Interests: catalog.NewSet(catalog.Interest("Basketball"))},
			b:    &Profile{Interests: catalog.NewSet(catalog.Interest("Basketball"))},
			want: 1.0,
		},
		{
			name: "exact match multiple",
			a: &Profile{Interests: catalog.NewSet(
				catalog.Interest("Basketball"), catalog.Interest("Tennis"),
			)},
			b: &Profile{Interests: catalog.NewSet(
				catalog.Interest("Basketball"), catalog.Interest("Tennis"),
			)},
			want: 1.0,
		},
		{
			name: "completely disjoint",
			a:    &Profile{Interests: catalog.NewSet(catalog.Interest("Basketball"))},
			b:    &Profile{Interests: catalog.NewSet(catalog.Interest("Tennis"))},
			want: 0,
		},
		{
			name: "partial overlap",
			a: &Profile{Interests: catalog.NewSet(
				catalog.Interest("Basketball"), catalog.Interest("Tennis"),
			)},
			b: &Profile{Interests: catalog.NewSet(
				catalog.Interest("Basketball"), catalog.Interest("Golf"),
			)},
			// A->B: basketball=1.0, tennis=0.0
			// B->A: basketball=1.0, golf=0.0
			// union = {basketball, tennis, golf} = 3
			// weightSum = 2.0, score = 2.0 / 6.0 = 0.333...
			want: 1.0 / 3.0,
		},
		{
			name: "one empty",
			a:    &Profile{Interests: catalog.NewSet(catalog.Interest("Basketball"))},
			b:    &Profile{},
			want: wildcardScore,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := Score(test.a, test.b)
			assert.InDelta(t, test.want, got, 0.001)
			// symmetry
			gotReverse := Score(test.b, test.a)
			assert.InDelta(t, test.want, gotReverse, 0.001, "symmetry check failed")
		})
	}
}
