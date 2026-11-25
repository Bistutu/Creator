package webwindow

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsWebsiteProcessIdentity(t *testing.T) {
	const fixture = "CREATOR_TEST_WEBSITE_PROCESS_IDENTITY"
	if os.Getenv(fixture) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestWindowsWebsiteProcessIdentity$")
		cmd.Env = append(os.Environ(), fixture+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated process identity check: %v\n%s", err, output)
		}
		return
	}
	if err := SetWindowIdentity("app-0011223344556677"); err != nil {
		t.Fatal(err)
	}
	read := windows.NewLazySystemDLL("shell32.dll").NewProc("GetCurrentProcessExplicitAppUserModelID")
	var value *uint16
	result, _, _ := read.Call(uintptr(unsafe.Pointer(&value)))
	if int32(result) < 0 || value == nil {
		t.Fatalf("GetCurrentProcessExplicitAppUserModelID: HRESULT 0x%08x", uint32(result))
	}
	defer windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(value)))
	if got, want := windows.UTF16PtrToString(value), "WebCrate.Creator.App0011223344556677"; got != want {
		t.Fatalf("process AppUserModelID %q, want %q", got, want)
	}
}
