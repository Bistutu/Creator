package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/unix"
)

func TestMacCreatorReceivesWebsiteLinkOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	dropPath := filepath.Join(state, "link-to-drop")
	t.Setenv("WEBCRATE_TEST_LINK_DROP", dropPath)
	store := profile.NewStore(filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename))
	const address = "https://www.xiaohongshu.com/explore/topic?from=creator#notes"
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.press("应用库")
		ui.control("创建第一个应用")
		sendMacLinkDrop(t, pid, dropPath, address, "accepted")
		ui.expectValue("网址或应用名称", address)
		ui.expectFocus("网址或应用名称")
		ui.control("使用网址 小红书")
		ui.capture("deskpad-dropped-website.png")
		ui.key("Enter")
		ui.expectValue("应用网址", address)
		ui.fill("应用名称", "Link Drop")
		sendMacLinkDrop(t, pid, dropPath, "https://chat.deepseek.com", "unaccepted")
		ui.expectValue("应用网址", address)
		ui.expectValue("应用名称", "Link Drop")
		ui.press("创建应用")
		ui.control("打开 Link Drop")
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].URL != address {
			t.Fatalf("drop followed by creation saved %+v (%v)", apps, err)
		}
		executable := filepath.Join(state, "Applications", "Creator", apps[0].ID, "Link Drop.app")
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", executable)
	})
	t.Log("An AppKit URL drop switched from the library to the selected website and the open editor kept its draft during another drop.")
}

func sendMacLinkDrop(t *testing.T, pid int, path, value, want string) {
	t.Helper()
	result := path + ".result"
	if err := os.Remove(result); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Kill(pid, unix.SIGINFO); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(1500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if status, err := os.ReadFile(result); err == nil && len(status) > 0 {
			if string(status) != want {
				t.Fatalf("AppKit drop status %q, want %q", status, want)
			}
			return
		}
	}
	t.Fatal("the isolated Cocoa drag fixture did not finish")
}
