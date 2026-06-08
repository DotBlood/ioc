//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reloadSignals returns the signals that trigger a live config reload. On
// unix-like systems that is SIGHUP, the conventional "reload your config"
// signal for a daemon.
func reloadSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}
