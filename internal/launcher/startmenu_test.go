package launcher

import (
	"fmt"
	"testing"
)

func TestStartMenuKeepsExistingShortcut(t *testing.T) {
	entries := map[string]startMenuEntry{
		"workbench (2).lnk": {owner: "existing"},
	}
	sameName := func(string) bool { return true }
	index := newStartMenuIndex(entries)
	if got := index.filename("Workbench", "existing", sameName); got != "Workbench (2).lnk" {
		t.Errorf("refresh moved an installed shortcut to %q", got)
	}
	if got := index.filename("Workbench", "new", sameName); got != "Workbench.lnk" {
		t.Errorf("new app did not use the free name: %q", got)
	}
	if got := index.filename("Research", "existing", sameName); got != "Research.lnk" {
		t.Errorf("renaming did not update the label: %q", got)
	}
	entries["workbench.lnk"] = startMenuEntry{owner: "new"}
	index = newStartMenuIndex(entries)
	if got := index.filename("Workbench", "third", sameName); got != "Workbench (3).lnk" {
		t.Errorf("a new app would replace another app's shortcut: %q", got)
	}
	if got := index.filename("Workbench", "new", sameName); got != "Workbench.lnk" {
		t.Errorf("refresh replaced the original shortcut: %q", got)
	}
}

func TestStartMenuKeepsNumberedShortcutsDistinctFromUserNames(t *testing.T) {
	entries := map[string]startMenuEntry{
		"工作台 (2).lnk":     {owner: "first"},
		"工作台 (2) (3).lnk": {owner: "second", path: "literal"},
		"工作台 (02).lnk":    {owner: "second"},
		"工作台 (8).lnk":     {owner: "second"},
		"工作台 (9).lnk":     {owner: "second"},
	}
	index := newStartMenuIndex(entries)
	if got := index.filename("工作台 (2)", "second", func(path string) bool { return path == "literal" }); got != "工作台 (2) (3).lnk" {
		t.Errorf("refresh moved a numbered shortcut for a literal name: %q", got)
	}
	if got := index.filename("工作台", "second", func(path string) bool { return path != "literal" }); got != "工作台 (8).lnk" {
		t.Errorf("refresh picked the wrong available entry: %q", got)
	}
	if got := index.filename("工作台", "first", func(string) bool { return false }); got != "工作台.lnk" {
		t.Errorf("renaming an app with literal digits left the old name: %q", got)
	}
}

func BenchmarkStartMenuPinnedName(b *testing.B) {
	for _, size := range []int{120, 1200} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			entries := make(map[string]startMenuEntry, size)
			for i := range size - 1 {
				entries[fmt.Sprintf("app%d.lnk", i)] = startMenuEntry{owner: fmt.Sprintf("owner%d", i)}
			}
			entries["workbench (2).lnk"] = startMenuEntry{owner: "tracked", path: "workbench (2).lnk"}
			index := newStartMenuIndex(entries)
			match := func(path string) bool { return path == "workbench (2).lnk" }
			b.ReportAllocs()
			for b.Loop() {
				if name := index.filename("Workbench", "tracked", match); name != "Workbench (2).lnk" {
					b.Fatal(name)
				}
			}
		})
	}
}
