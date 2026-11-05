package app

import (
	"os/exec"
	"strings"
	"syscall"
)

// speak reads text aloud with the Windows speech synthesizer. The text goes
// in on standard input, never on the command line.
func speak(text string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Add-Type -AssemblyName System.Speech; $s = New-Object System.Speech.Synthesis.SpeechSynthesizer; $s.Speak([Console]::In.ReadToEnd())")
	cmd.Stdin = strings.NewReader(text)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if out, err := cmd.CombinedOutput(); err != nil {
		return speakErr(err, out)
	}
	return nil
}
