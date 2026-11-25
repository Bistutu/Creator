package webwindow

import (
	"slices"
	"testing"
	"time"
)

func TestHomeCommandUsesLatestRequestAndKeepsLastKnownAddress(t *testing.T) {
	requests := make(chan chan string, 1)
	updates := make(chan func(), 1)
	home := homeCommand{
		address: "bundled",
		read: func() string {
			result := make(chan string, 1)
			requests <- result
			select {
			case address := <-result:
				return address
			case <-t.Context().Done():
				return ""
			}
		},
		dispatch: func(f func()) { updates <- f },
	}
	var visited []string
	visit := func(address string) { visited = append(visited, address) }
	complete := func(address string) {
		t.Helper()
		waitForHomeValue(t, requests) <- address
		waitForHomeValue(t, updates)()
	}

	home.open(visit)
	complete("")
	home.open(visit)
	old := waitForHomeValue(t, requests)
	home.open(visit)
	home.open(visit)
	select {
	case <-requests:
		t.Fatal("two home reads ran at once")
	default:
	}
	old <- "outdated"
	waitForHomeValue(t, updates)()
	complete("committed")
	home.open(visit)
	home.cancel()
	complete("canceled")
	home.open(visit)
	complete("")
	if want := []string{"bundled", "committed", "committed"}; !slices.Equal(visited, want) {
		t.Fatalf("visited %v, want %v", visited, want)
	}
}

func waitForHomeValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("waiting for the home command")
		var zero T
		return zero
	}
}
