package view

// selectOption is a value/label pair for select elements.
type selectOption struct {
	Value string
	Label string
}

// Genders is the list of gender options for the demographics form.
var Genders = []selectOption{
	{Value: "male", Label: "Male"},
	{Value: "female", Label: "Female"},
	{Value: "gender-fluid", Label: "Gender Fluid"},
	{Value: "nonbinary", Label: "Non-Binary"},
	{Value: "agender", Label: "Agender"},
	{Value: "other", Label: "Other"},
}

// Roles is the list of role options for the demographics form.
var Roles = []selectOption{
	{Value: "dominant", Label: "Dominant"},
	{Value: "submissive", Label: "Submissive"},
	{Value: "switch", Label: "Switch"},
}

// InterestGroup is a named category of interests.
type InterestGroup struct {
	Name  string
	Items []string
}

// InterestGroups is the categorized list of interests for the
// demographics form. Placeholder sports categories for now.
var InterestGroups = []InterestGroup{
	{
		Name: "Team Sports",
		Items: []string{
			"basketball",
			"soccer",
			"baseball",
			"hockey",
			"volleyball",
		},
	},
	{
		Name: "Individual Sports",
		Items: []string{
			"tennis",
			"swimming",
			"track",
			"golf",
			"cycling",
		},
	},
}
