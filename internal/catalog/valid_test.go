package catalog

import (
	"testing"

	"github.com/silirim/vodmax/internal/playlist/xtream"
)

// A broken line of the provider's list is not a channel or a film.
func TestValidEntry(t *testing.T) {
	cases := []struct {
		name, id xtream.Flex
		want     bool
	}{{"Rai 1", "101", true}, {"", "101", false}, {`["l"]`, "101", false}, {"Rai 1", "abc", false}, {"Rai 1", `{"a":1}`, false}}
	for _, c := range cases {
		if got := validEntry(c.name, c.id); got != c.want {
			t.Errorf("%q %q = %v", c.name, c.id, got)
		}
	}
}
