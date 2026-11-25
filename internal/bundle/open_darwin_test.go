package bundle

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"
)

func TestWorkspaceOpenUsesNSErrorStatus(t *testing.T) {
	if err := loadAppKit(); err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	newError := func(domain string, code int, cause objc.ID) objc.ID {
		var userInfo objc.ID
		if cause != 0 {
			userInfo = objc.ID(objc.GetClass("NSDictionary")).Send(objc.RegisterName("dictionaryWithObject:forKey:"), cause, foundationString("NSUnderlyingError"))
		}
		return objc.ID(objc.GetClass("NSError")).Send(objc.RegisterName("errorWithDomain:code:userInfo:"), foundationString(domain), code, userInfo)
	}
	const osStatus = "NSOSStatusErrorDomain"
	for _, tc := range []struct {
		name    string
		failure objc.ID
		domain  string
		retry   bool
	}{
		{"process quit", newError(osStatus, -600, 0), osStatus, true},
		{"connection ended", newError(osStatus, -609, 0), osStatus, true},
		{"nested status", newError("NSCocoaErrorDomain", 256, newError(osStatus, -600, 0)), "NSCocoaErrorDomain", true},
		{"app missing", newError(osStatus, -10814, 0), osStatus, false},
		{"other domain", newError("com.creator.fixture", -600, 0), "com.creator.fixture", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := readWorkspaceError(tc.failure)
			if failure.lostProcess != tc.retry || failure.domain != tc.domain || !strings.Contains(failure.Error(), tc.domain) {
				t.Fatalf("error classification = %+v, want domain %s and retry %t", failure, tc.domain, tc.retry)
			}
		})
	}
}

func TestWorkspaceOpenRetriesOnlyTransientFailures(t *testing.T) {
	transient := &workspaceOpenError{domain: "NSOSStatusErrorDomain", code: -600, lostProcess: true}
	permanent := &workspaceOpenError{domain: "NSOSStatusErrorDomain", code: -10814}
	for _, tc := range []struct {
		name     string
		attempts []error
		want     error
	}{
		{"already running", []error{nil}, nil},
		{"reopen after quit", []error{transient, nil}, nil},
		{"app missing", []error{permanent}, permanent},
		{"process stays unavailable", []error{transient, transient, transient}, transient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryWorkspaceOpen(t.Context(), func() error {
				if calls == len(tc.attempts) {
					t.Fatal("launch was retried more than expected")
				}
				failure := tc.attempts[calls]
				calls++
				return failure
			})
			if !errors.Is(err, tc.want) || calls != len(tc.attempts) {
				t.Fatalf("launch returned %v after %d attempts", err, calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	if err := retryWorkspaceOpen(ctx, func() error {
		calls++
		cancel()
		return transient
	}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceled launch returned %v after %d attempts", err, calls)
	}
	if err := retryWorkspaceOpen(ctx, func() error {
		t.Fatal("an already canceled operation tried to start an application")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled launch returned %v", err)
	}
}

func TestWorkspaceOpenReportsMissingApplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := (darwinBackend{}).Open(ctx, filepath.Join(t.TempDir(), "Missing.app"))
	var failure *workspaceOpenError
	if !errors.As(err, &failure) || failure.lostProcess {
		t.Fatalf("opening a missing app returned %v", err)
	}
}
