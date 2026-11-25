package launcher

import (
	"fmt"
	"strconv"
	"strings"
)

type startMenuEntry struct {
	path  string
	owner string
}

type startMenuIndex struct {
	byName  map[string]startMenuEntry
	byOwner map[string][]string
}

func newStartMenuIndex(entries map[string]startMenuEntry) startMenuIndex {
	index := startMenuIndex{byName: entries, byOwner: make(map[string][]string)}
	for name, entry := range entries {
		if entry.owner != "" {
			index.byOwner[entry.owner] = append(index.byOwner[entry.owner], name)
		}
	}
	return index
}

func (index startMenuIndex) filename(stem, id string, sameExecutableName func(string) bool) string {
	name := stem + ".lnk"
	if index.byName[strings.ToLower(name)].owner == id {
		return name
	}
	// Keep an existing shortcut at its original number even if a lower one
	// becomes available. The system or the user may have pinned this file.
	prefix := strings.ToLower(stem) + " ("
	savedSuffix := 0
	for _, filename := range index.byOwner[id] {
		if !strings.HasPrefix(filename, prefix) || !strings.HasSuffix(filename, ").lnk") {
			continue
		}
		digits := strings.TrimSuffix(strings.TrimPrefix(filename, prefix), ").lnk")
		number, err := strconv.Atoi(digits)
		if err == nil && number >= 2 && strconv.Itoa(number) == digits && (savedSuffix == 0 || number < savedSuffix) && sameExecutableName(index.byName[filename].path) {
			savedSuffix = number
		}
	}
	if savedSuffix != 0 {
		return fmt.Sprintf("%s (%d).lnk", stem, savedSuffix)
	}
	for suffix := 2; ; suffix++ {
		if current, exists := index.byName[strings.ToLower(name)]; !exists || current.owner == id {
			return name
		}
		name = fmt.Sprintf("%s (%d).lnk", stem, suffix)
	}
}
