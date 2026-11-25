//go:build !linux && !windows

package webwindow

func SetWindowIdentity(string) error { return nil }
