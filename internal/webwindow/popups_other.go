//go:build !linux

package webwindow

import "github.com/egoist/mygo"

func BindRelatedPopups(*mygo.Window, *Downloads) bool { return false }
