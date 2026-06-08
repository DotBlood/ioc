//go:build windows

package main

import "os"

// reloadSignals returns no signals on Windows: there is no SIGHUP. A live config
// reload via signal is therefore unavailable; restart the daemon to pick up
// config changes. (The serve loop calls this and simply skips signal-based
// reload when the slice is empty.)
func reloadSignals() []os.Signal {
	return nil
}
