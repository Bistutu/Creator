//go:build creator_e2e && !mygo_noinspector

package main

import (
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

func listenForFixtureLinkDrop(path string) {
	drops := make(chan os.Signal, 1)
	signal.Notify(drops, syscall.SIGINFO)
	go func() {
		for range drops {
			value, err := os.ReadFile(path)
			status := "unaccepted"
			if err == nil {
				mygo.RunOnMain(func() {
					var accepted bool
					accepted, err = dropFixtureLink(string(value))
					if accepted {
						status = "accepted"
					}
				})
			}
			if err != nil {
				status = err.Error()
			}
			_ = os.WriteFile(path+".result", []byte(status), 0o600)
		}
	}()
}

type macDragPoint struct{ X, Y float64 }

// The fixture is called on the main thread with its own pasteboard and
// AppKit drag destination, leaving the system clipboard and pointer alone.
var fixtureLinkDrag struct {
	point macDragPoint
	board objc.ID
}

var fixtureLinkDragClass = sync.OnceValues(func() (objc.Class, error) {
	sel := objc.RegisterName
	return objc.RegisterClass("CreatorTestLinkDrag", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: sel("draggingLocation"), Fn: func(objc.ID, objc.SEL) macDragPoint { return fixtureLinkDrag.point }},
		{Cmd: sel("draggingPasteboard"), Fn: func(objc.ID, objc.SEL) objc.ID { return fixtureLinkDrag.board }},
		{Cmd: sel("draggingSourceOperationMask"), Fn: func(objc.ID, objc.SEL) uint { return 1 }},
	})
})

func dropFixtureLink(value string) (bool, error) {
	sel := objc.RegisterName
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	defer pool.Send(sel("drain"))
	var view objc.ID
	for _, window := range mygo.Windows() {
		if window.Title() == appName {
			subviews := objc.ID(window.NativeHandle()).Send(sel("contentView")).Send(sel("subviews"))
			for i, count := uint(0), objc.Send[uint](subviews, sel("count")); i < count; i++ {
				candidate := subviews.Send(sel("objectAtIndex:"), i)
				if candidate.Send(sel("isKindOfClass:"), objc.GetClass("MyGoSurfaceView")) != 0 {
					view = candidate
					break
				}
			}
			break
		}
	}
	if view == 0 {
		return false, errors.New("missing Creator drag destination")
	}
	cls, err := fixtureLinkDragClass()
	if err != nil {
		return false, err
	}
	info := objc.ID(cls).Send(sel("new"))
	defer info.Send(sel("release"))
	const copyOperation = 1
	board := objc.ID(objc.GetClass("NSPasteboard")).Send(sel("pasteboardWithUniqueName"))
	defer board.Send(sel("releaseGlobally"))
	item := objc.ID(objc.GetClass("NSPasteboardItem")).Send(sel("new"))
	defer item.Send(sel("release"))
	nsString := func(value string) objc.ID {
		return objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), value)
	}
	item.Send(sel("setString:forType:"), nsString(value), nsString("public.url"))
	items := objc.ID(objc.GetClass("NSArray")).Send(sel("arrayWithObject:"), item)
	if !objc.Send[bool](board, sel("writeObjects:"), items) {
		return false, errors.New("write the link to the fixture pasteboard")
	}
	fixtureLinkDrag.board = board
	fixtureLinkDrag.point = objc.Send[macDragPoint](view, sel("convertPoint:toView:"), macDragPoint{X: 128, Y: 152}, objc.ID(0))
	defer func() { fixtureLinkDrag.board = 0 }()
	defer view.Send(sel("draggingExited:"), info)
	return objc.Send[uint](view, sel("draggingEntered:"), info) == copyOperation &&
		objc.Send[bool](view, sel("performDragOperation:"), info), nil
}
