//go:build !windows

package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// speak reads text aloud with espeak-ng or espeak (sudo apt install
// espeak-ng). The text goes in on standard input.
func speak(text string) error {
	for _, prog := range []string{"espeak-ng", "espeak"} {
		path, err := exec.LookPath(prog)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, "--stdin")
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return speakErr(err, out)
		}
		return nil
	}
	return fmt.Errorf("no speech program found: install espeak-ng (sudo apt install espeak-ng)")
}
