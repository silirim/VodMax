package catalog

import (
	"os"
	"os/exec"
	"testing"
)

// The country table must resolve the same way at every start,
// and every country offered for the first choice must be accepted.
func TestCountryTableStable(t *testing.T) {
	if os.Getenv("VODMAX_COUNTRY_CHILD") == "1" {
		for _, code := range HomeCountries() {
			if !IsHomeCountry(code) {
				t.Fatalf("HomeCountries offers %q but IsHomeCountry rejects it", code)
			}
		}
		if !IsHomeCountry("AR") {
			t.Fatal("Argentina must be a home country")
		}
		os.Stdout.WriteString("AR|" + DetectCountry("movie", "AR | Film") + "|" + DetectCountry("live", "Live Arabia") + "\n")
		return
	}
	var first string
	for i := 0; i < 25; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCountryTableStable$", "-test.v")
		cmd.Env = append(os.Environ(), "VODMAX_COUNTRY_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("start %d: %v\n%s", i, err, out)
		}
		var line string
		for _, l := range splitOutputLines(string(out)) {
			if len(l) > 3 && l[:3] == "AR|" {
				line = l
			}
		}
		if line == "" || line[len(line)-len("|ARABIC"):] != "|ARABIC" {
			t.Fatalf("start %d: unexpected detection %q", i, line)
		}
		if i == 0 {
			first = line
		} else if line != first {
			t.Fatalf("detection changes between starts: %q then %q", first, line)
		}
	}
}

func splitOutputLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
