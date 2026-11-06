// Package packaging holds no Go code: its tests run the shell scripts
// shipped next to the server.
package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vodmax-update's choice of whether to install, run on the real script
// with curl and the server binary faked: nothing leaves the machine, and
// a chosen release stops at the (failing) download, before installing.
func TestUpdateVersionChoice(t *testing.T) {
	for _, tool := range []string{"sh", "tar", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	script, err := filepath.Abs("vodmax-update")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		installed, latest, arg, want string
	}{
		{"0.6.6", "v0.6.5", "", "più recente di v0.6.5"},
		{"0.6.10", "v0.6.9", "", "più recente di v0.6.9"}, // version order, not text order
		{"0.6.6", "v0.6.6", "", "v0.6.6 è già installato"},
		{"0.6.6", "v0.7.0", "", "Scarico VodMax v0.7.0"},
		{"0.6.0-beta.2", "v0.6.0", "", "Scarico VodMax v0.6.0"}, // a beta moves on to its release
		{"0.6.6", "", "0.6.5", "Scarico VodMax v0.6.5"},         // a named version may go back
		{"", "v0.6.6", "", "Scarico VodMax v0.6.6"},             // nothing installed yet
	}
	for _, c := range cases {
		dir := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// The last argument is the address: GitHub's API answers, every
		// download fails.
		write("curl", `for a; do u=$a; done
case $u in *api.github.com*) printf '{"tag_name": "%s"}\n' "$LATEST" ;; *) exit 22 ;; esac
`)
		bin := filepath.Join(dir, "vodmax-server")
		if c.installed != "" {
			write("vodmax-server", "echo vodmax-server "+c.installed+"\n")
		}
		args := []string{script}
		if c.arg != "" {
			args = append(args, c.arg)
		}
		cmd := exec.Command("sh", args...)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "VODMAX_BIN="+bin, "LATEST="+c.latest, "BETA=", "FORCE=")
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), c.want) {
			t.Errorf("installed %q, latest %q, arg %q: want %q in:\n%s", c.installed, c.latest, c.arg, c.want, out)
		}
		if strings.Contains(string(out), "Scarico") && !strings.Contains(string(out), "non trovata") {
			t.Errorf("installed %q: went past the faked download:\n%s", c.installed, out)
		}
	}
}
