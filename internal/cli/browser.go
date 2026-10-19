package cli

import (
	"os/exec"
	"runtime"
)

// browserCommand is the command that opens url in the user's browser on goos.
func browserCommand(goos, url string) *exec.Cmd {
	switch goos {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		return exec.Command("open", url)
	}
	return exec.Command("xdg-open", url)
}

// openBrowser starts the browser on url and does not wait for it to exit.
func openBrowser(url string) error {
	cmd := browserCommand(runtime.GOOS, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
