//go:build !windows

package webwindow

// MyGo already uses the configured XDG directory on Linux.
func UseSystemDownloadsFolder() {}

// The staging directory uses a dot prefix.
func hideDownloadDirectory(string) {}
