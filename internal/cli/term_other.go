//go:build !windows && !linux

package cli

import "os"

// readSecret reads a line; echo control is not implemented here.
func readSecret(f *os.File) (string, error) { return readLine(f) }

// terminalWidth is unknown on this platform.
func terminalWidth(*os.File) int { return 0 }
