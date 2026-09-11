//go:build !windows && !unix

package appserverdaemon

// reapZombiePIDProcess has no portable implementation outside Unix platforms.
func reapZombiePIDProcess(pid uint32) {}
