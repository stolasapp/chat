package catalog

import "fmt"

// Species identifies a furry species/fursona type. The underlying
// string is the display label; the wire format is derived
// (lowercase, spaces to underscores).
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Species string

// SpeciesOther is the default species, omitted from match
// notifications.
const SpeciesOther Species = "Other"

// Label returns the human-readable label.
func (s Species) Label() string { return string(s) }

// String returns the wire format.
func (s Species) String() string { return toWire(string(s)) }

// MarshalText implements encoding.TextMarshaler.
func (s Species) MarshalText() ([]byte, error) {
	if !speciesSet[s] {
		return nil, fmt.Errorf("invalid species: %q", s)
	}
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. Unknown
// values are silently ignored (species resets to zero value).
func (s *Species) UnmarshalText(text []byte) error {
	if parsed, ok := speciesLookup[string(text)]; ok {
		*s = parsed
	}
	return nil
}

// AllSpecies returns all species values sorted by label.
func AllSpecies() []Species { return species }

// species is the canonical list. Keep sorted by label.
var species = []Species{
	"Alien",
	"Angel",
	"Ape",
	"Armadillo",
	"Badger",
	"Bat",
	"Bear",
	"Beaver",
	"Bee",
	"Bird",
	"Bison",
	"Boar",
	"Bull",
	"Cat",
	"Centaur",
	"Chameleon",
	"Cheetah",
	"Chimera",
	"Chinchilla",
	"Cougar",
	"Cow",
	"Coyote",
	"Crocodile",
	"Crow",
	"Deer",
	"Demon",
	"Digimon",
	"Dinosaur",
	"Dog",
	"Dolphin",
	"Donkey",
	"Dragon",
	"Eagle",
	"Elk",
	"Ferret",
	"Fish",
	"Fox",
	"Gecko",
	"Goat",
	"Gorilla",
	"Gryphon",
	"Hamster",
	"Hare",
	"Hawk",
	"Hedgehog",
	"Horse",
	"Human",
	"Hybrid",
	"Hyena",
	"Jackal",
	"Jay",
	"Kangaroo",
	"Kitsune",
	"Koala",
	"Leopard",
	"Lion",
	"Lizard",
	"Lynx",
	"Mantis",
	"Mink",
	"Monkey",
	"Moose",
	"Moth",
	"Mouse",
	SpeciesOther,
	"Opossum",
	"Orca",
	"Octopus",
	"Owl",
	"Panda",
	"Panther",
	"Parrot",
	"Penguin",
	"Phoenix",
	"Pig",
	"Plant",
	"Polar Bear",
	"Pokemon",
	"Protogen",
	"Rabbit",
	"Raccoon",
	"Raptor",
	"Rat",
	"Raven",
	"Red Panda",
	"Reindeer",
	"Robot",
	"Satyr",
	"Scorpion",
	"Seal",
	"Sergal",
	"Shark",
	"Sheep",
	"Skunk",
	"Slime",
	"Snake",
	"Snow Leopard",
	"Spider",
	"Sphinx",
	"Squirrel",
	"Synth",
	"Tanuki",
	"Tiger",
	"Turtle",
	"Unicorn",
	"Weasel",
	"Werewolf",
	"Whale",
	"Wolf",
	"Wolverine",
	"Zebra",
}

var speciesSet = buildSet(species)
var speciesLookup = buildLookup(species)
