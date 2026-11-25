package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkStoreEditAndRead(b *testing.B) {
	for _, count := range []int{120, 1200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			apps := make([]App, count)
			for i := range apps {
				apps[i] = App{
					ID: NewID(), Name: fmt.Sprintf("Workspace %d", i), URL: fmt.Sprintf("https://example.org/desk/%d", i),
					Width: 880, Height: 640, Category: "效率", Color: DefaultColor,
				}
			}
			data, err := json.Marshal(apps)
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(b.TempDir(), "apps.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				b.Fatal(err)
			}
			store, changing := NewStore(path), apps[len(apps)/2]
			if _, err := store.Find(changing.ID); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				changing.Width = 1761 - changing.Width
				if err := store.Put(changing); err != nil {
					b.Fatal(err)
				}
				if saved, err := store.Find(changing.ID); err != nil || saved != changing {
					b.Fatalf("reading the edited app: %+v (%v)", saved, err)
				}
			}
		})
	}
}
