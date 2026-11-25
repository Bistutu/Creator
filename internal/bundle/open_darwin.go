package bundle

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var loadAppKit = sync.OnceValue(func() error {
	_, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	return err
})

func (darwinBackend) Open(ctx context.Context, path string) error {
	return retryWorkspaceOpen(ctx, func() error {
		if err := loadAppKit(); err != nil {
			return err
		}
		return openInWorkspace(ctx, path)
	})
}

func openInWorkspace(ctx context.Context, path string) error {
	result := make(chan error, 1)
	// The request may be issued from a library worker. Keep its autorelease
	// pool on the same thread. AppKit calls the completion on its own queue.
	runtime.LockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	workspace := objc.ID(objc.GetClass("NSWorkspace")).Send(objc.RegisterName("sharedWorkspace"))
	url := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("fileURLWithPath:isDirectory:"), foundationString(path), true)
	configuration := objc.ID(objc.GetClass("NSWorkspaceOpenConfiguration")).Send(objc.RegisterName("configuration"))
	// The library displays launch errors. Keep AppKit's defaults for
	// activation and reuse of an existing instance.
	configuration.Send(objc.RegisterName("setPromptsUserIfNeeded:"), false)
	completion := objc.NewBlock(func(_ objc.Block, app, failure objc.ID) {
		switch {
		case failure != 0:
			result <- readWorkspaceError(failure)
		case app == 0:
			result <- errors.New("macOS 未确认应用已启动")
		default:
			result <- nil
		}
	})
	workspace.Send(objc.RegisterName("openApplicationAtURL:configuration:completionHandler:"), url, configuration, completion)
	completion.Release()
	pool.Send(objc.RegisterName("drain"))
	runtime.UnlockOSThread()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryWorkspaceOpen(ctx context.Context, open func() error) error {
	const retries = 2
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := open()
		var failure *workspaceOpenError
		if attempt == retries || !errors.As(err, &failure) || !failure.lostProcess {
			return err
		}
		// Launch Services can still hold a reference to a process just after
		// it quits. Request the same app again once that reference clears.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type workspaceOpenError struct {
	description string
	domain      string
	code        int
	lostProcess bool
}

func (e *workspaceOpenError) Error() string {
	return fmt.Sprintf("%s (%s %d)", e.description, e.domain, e.code)
}

func readWorkspaceError(failure objc.ID) *workspaceOpenError {
	const (
		procNotFound      = -600
		connectionInvalid = -609
	)
	stringValue := func(value objc.ID) string {
		return objc.Send[string](value, objc.RegisterName("UTF8String"))
	}
	domain := objc.RegisterName("domain")
	code := objc.RegisterName("code")
	err := &workspaceOpenError{
		description: stringValue(failure.Send(objc.RegisterName("localizedDescription"))),
		domain:      stringValue(failure.Send(domain)),
		code:        objc.Send[int](failure, code),
	}
	underlying := foundationString("NSUnderlyingError")
	for current, depth := failure, 0; current != 0 && depth < 8; depth++ {
		status := objc.Send[int](current, code)
		if (status == procNotFound || status == connectionInvalid) && stringValue(current.Send(domain)) == "NSOSStatusErrorDomain" {
			err.lostProcess = true
			break
		}
		info := current.Send(objc.RegisterName("userInfo"))
		current = info.Send(objc.RegisterName("objectForKey:"), underlying)
	}
	return err
}

func foundationString(value string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), value)
}
