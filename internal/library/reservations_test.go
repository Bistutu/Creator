package library

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestAddressReservationReleasesFirstKeyOnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var addresses reservations
		releaseSecond, err := addresses.acquire(t.Context(), "second")
		if err != nil {
			t.Fatal(err)
		}
		defer releaseSecond()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		finished := make(chan error, 1)
		go func() {
			release, err := addresses.acquirePair(ctx, "second", "first")
			if err == nil {
				release()
			}
			finished <- err
		}()
		synctest.Wait()
		if release, busy := addresses.claim("first"); busy == nil {
			release()
			t.Fatal("the move did not reserve the first address before waiting")
		}
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled reservation returned %v", err)
		}
		release, busy := addresses.claim("first")
		if busy != nil {
			t.Fatal("cancelled reservation left the first address locked")
		}
		release()
	})
}
