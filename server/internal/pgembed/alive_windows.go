//go:build windows

package pgembed

import "os"

// FindProcess opens a handle on Windows, so it fails for a gone process.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
