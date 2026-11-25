package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRuntimeLinksAndUpdatesOneAppAtATime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner.exe")
	cache := filepath.Join(root, "Runtimes")
	writeRuntimeFixture(t, source, []byte("creator runner version a"))
	requireFixtureHardLinks(t, source)
	runner := openRuntimeFixture(t, source, cache)
	first := runtimeDestination(t, root, "first")
	second := runtimeDestination(t, root, "second")
	for _, path := range []string{first, second} {
		if err := runner.install(t.Context(), path); err != nil {
			t.Fatal(err)
		}
	}
	if !shareFile(t, first, second) || !shareFile(t, first, runner.shared) || shareFile(t, source, first) {
		t.Fatal("the two generated executables should share their own cached file")
	}

	settings := filepath.Join(filepath.Dir(first), ConfigName)
	writeRuntimeFixture(t, settings, []byte(`{"name":"Work","url":"https://example.org/work"}`))
	settingsBefore, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, source, []byte("creator runner version b"))
	next := openRuntimeFixture(t, source, cache)
	if err := next.refresh(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	assertFile(t, first, "creator runner version b")
	assertFile(t, second, "creator runner version a")
	if shareFile(t, first, second) {
		t.Fatal("updating an app changed the previous runners' file identity")
	}
	assertFile(t, settings, string(settingsBefore))

	// Closing and reopening Creator can finish a migration interrupted earlier.
	// Legacy applications have an executable without a runtime marker.
	if err := os.Remove(filepath.Join(filepath.Dir(second), runtimeMarker)); err != nil {
		t.Fatal(err)
	}
	next = openRuntimeFixture(t, source, cache)
	if err := next.refresh(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := next.refresh(t.Context(), second); err != nil || !shareFile(t, first, second) {
		t.Fatalf("the second executable did not join the new version: %v", err)
	}
	if err := os.Remove(next.shared); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	assertFile(t, second, "creator runner version b")
}

func TestRuntimeCopiesWhenHardLinksAreUnavailable(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	writeRuntimeFixture(t, source, []byte("previous runtime"))
	prev := openRuntimeFixture(t, source, cache)
	prev.link = refuseLink
	app := runtimeDestination(t, root, "standalone")
	if err := prev.install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	if shareFile(t, source, app) {
		t.Fatal("the fallback should copy the executable")
	}
	if _, err := os.Stat(prev.shared); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback left a second unused runtime copy: %v", err)
	}

	writeRuntimeFixture(t, source, []byte("updated runtime"))
	next := openRuntimeFixture(t, source, cache)
	next.link = refuseLink
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := next.refresh(ctx, app); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh: %v", err)
	}
	assertFile(t, app, "previous runtime")
	if err := next.refresh(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	assertFile(t, app, "updated runtime")
}

func TestRuntimeRejectsStaleSharedFileDuringLink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	writeRuntimeFixture(t, source, []byte("current bytes"))
	requireFixtureHardLinks(t, source)
	runner := openRuntimeFixture(t, source, filepath.Join(root, "Runtimes"))
	original := runtimeDestination(t, root, "original")
	if err := runner.install(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(root, "old")
	writeRuntimeFixture(t, older, []byte("expired bytes"))
	runner.link = func(from, to string) error {
		if from == runner.shared {
			if err := os.Rename(older, runner.shared); err != nil {
				t.Fatal(err)
			}
		}
		return os.Link(from, to)
	}
	second := runtimeDestination(t, root, "second")
	if err := runner.install(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	assertFile(t, second, "current bytes")
	assertFile(t, original, "current bytes")
	if !shareFile(t, second, runner.shared) {
		t.Fatal("cache did not return to the expected runtime")
	}
}

func TestRuntimeRetriesAnUnavailableSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	if _, err := newRuntimeFile(source, cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source was accepted: %v", err)
	}
	writeRuntimeFixture(t, source, nil)
	if _, err := newRuntimeFile(source, cache); err == nil {
		t.Fatal("empty source was accepted")
	}
	writeRuntimeFixture(t, source, []byte("runner"))
	runner := openRuntimeFixture(t, source, cache)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	app := runtimeDestination(t, root, "standalone")
	if err := runner.install(t.Context(), app); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source succeeded: %v", err)
	}
	writeRuntimeFixture(t, source, []byte("runner"))
	writeRuntimeFixture(t, source+".sha256", []byte("invalid digest"))
	if err := runner.install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	assertFile(t, app, "runner")
	assertFile(t, filepath.Join(filepath.Dir(app), runtimeMarker), fmt.Sprintf("%x", sha256.Sum256([]byte("runner"))))
}

func TestRuntimeChecksAnInstalledVersionUsingThePackageDigest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	data := []byte("packaged runtime")
	writeRuntimeFixture(t, source, data)
	writeRuntimeFixture(t, source+".sha256", fmt.Appendf(nil, "%x", sha256.Sum256(data)))
	app := runtimeDestination(t, root, "installed")
	first := openRuntimeFixture(t, source, cache)
	if err := first.install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	next := openRuntimeFixture(t, source, cache)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := next.refresh(t.Context(), app); err != nil {
		t.Fatalf("checking an already installed runner reached the missing binary: %v", err)
	}
	assertFile(t, app, string(data))
	assertFile(t, filepath.Join(filepath.Dir(app), runtimeMarker), first.digest)
}

func TestRuntimeCurrentChecksContinueDuringRepair(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	data := []byte("packaged runtime")
	writeRuntimeFixture(t, source, data)
	writeRuntimeFixture(t, source+".sha256", fmt.Appendf(nil, "%x", sha256.Sum256(data)))
	requireFixtureHardLinks(t, source)
	runner := openRuntimeFixture(t, source, filepath.Join(root, "Runtimes"))
	healthy := runtimeDestination(t, root, "healthy")
	repairing := runtimeDestination(t, root, "repairing")
	for _, app := range []string{healthy, repairing} {
		if err := runner.install(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(filepath.Dir(repairing), runtimeMarker)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	waiting, resume := pauseSharedRuntimeLink(t, runner)
	var tasks sync.WaitGroup
	defer func() {
		resume()
		tasks.Wait()
	}()
	repaired := make(chan error, 1)
	tasks.Go(func() { repaired <- runner.refresh(t.Context(), repairing) })
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("the repair did not reach the shared runtime installation")
	}
	checked := make(chan error, 1)
	tasks.Go(func() { checked <- runner.refresh(t.Context(), healthy) })
	select {
	case err := <-checked:
		if err != nil {
			t.Fatalf("checking the healthy runtime: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the healthy app waited for another app's runtime repair")
	}

	unpublished := runtimeDestination(t, root, "canceled")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	canceled := make(chan error, 1)
	tasks.Go(func() { canceled <- runner.install(ctx, unpublished) })
	select {
	case err := <-canceled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the queued runtime install returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a canceled install waited for the runtime repair")
	}
	for _, path := range []string{unpublished, filepath.Join(filepath.Dir(unpublished), runtimeMarker)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the canceled install published %q: %v", path, err)
		}
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the paused repair published a marker: %v", err)
	}
	resume()
	if err := <-repaired; err != nil {
		t.Fatal(err)
	}
	assertFile(t, marker, runner.digest)
	if !shareFile(t, healthy, repairing) {
		t.Fatal("the repaired app did not reuse the complete shared executable")
	}
	if err := runner.install(t.Context(), unpublished); err != nil {
		t.Fatalf("retrying the canceled install: %v", err)
	}
	assertFile(t, unpublished, string(data))
}

func pauseSharedRuntimeLink(t *testing.T, runner *runtimeFile) (<-chan struct{}, context.CancelFunc) {
	t.Helper()
	paused, resume := context.WithCancel(t.Context())
	waiting := make(chan struct{})
	link := runner.link
	var announce sync.Once
	runner.link = func(from, to string) error {
		if from == runner.shared {
			announce.Do(func() { close(waiting) })
			<-paused.Done()
		}
		return link(from, to)
	}
	return waiting, resume
}

func TestRuntimeRepairsTruncatedExecutableWithCurrentMarker(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "launcher")
	runtime := []byte("complete executable")
	writeRuntimeFixture(t, source, runtime)
	requireFixtureHardLinks(t, source)
	cache := filepath.Join(root, "Runtimes")
	first := runtimeDestination(t, root, "first")
	second := runtimeDestination(t, root, "second")
	runner := openRuntimeFixture(t, source, cache)
	for _, app := range []string{first, second} {
		if err := runner.install(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	if !shareFile(t, first, second) {
		t.Fatal("the fixtures did not use the shared executable")
	}
	if err := os.Truncate(second, int64(len(runtime)-1)); err != nil {
		t.Fatal(err)
	}
	restarted := openRuntimeFixture(t, source, cache)
	for _, app := range []string{first, second} {
		assertFile(t, filepath.Join(filepath.Dir(app), runtimeMarker), runner.digest)
		if err := restarted.refresh(t.Context(), app); err != nil {
			t.Fatal(err)
		}
		assertFile(t, app, string(runtime))
	}
	if !shareFile(t, first, second) || !shareFile(t, restarted.shared, second) {
		t.Fatal("repair should reuse the complete shared executable")
	}
}

func TestRuntimeDoesNotPublishBytesThatDisagreeWithThePackageDigest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	old := []byte("previous runtime")
	writeRuntimeFixture(t, source, old)
	app := runtimeDestination(t, root, "installed")
	previous := openRuntimeFixture(t, source, cache)
	if err := previous.install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	updated := []byte("expected runtime")
	writeRuntimeFixture(t, source, []byte("modified runtime"))
	writeRuntimeFixture(t, source+".sha256", fmt.Appendf(nil, "%x", sha256.Sum256(updated)))
	replacement := openRuntimeFixture(t, source, cache)
	if err := replacement.refresh(t.Context(), app); !errors.Is(err, errBundledRuntimeChanged) {
		t.Fatalf("replacing the runner with mismatched bytes returned %v", err)
	}
	marker := filepath.Join(filepath.Dir(app), runtimeMarker)
	assertFile(t, app, string(old))
	assertFile(t, marker, previous.digest)
	writeRuntimeFixture(t, source, updated)
	if err := replacement.refresh(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	assertFile(t, app, string(updated))
	assertFile(t, marker, replacement.digest)
}

func TestRuntimeReplacesAnOversizedMarkerAndKeepsAConflictingDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	writeRuntimeFixture(t, source, []byte("previous runtime"))
	app := runtimeDestination(t, root, "installed")
	if err := openRuntimeFixture(t, source, cache).install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(app), runtimeMarker)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(marker, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, filepath.Join(marker, "keep.txt"), []byte("retained data"))
	writeRuntimeFixture(t, source, []byte("updated runtime"))
	next := openRuntimeFixture(t, source, cache)
	if err := next.refresh(t.Context(), app); err == nil {
		t.Fatal("refresh succeeded with a directory at the marker path")
	}
	assertFile(t, app, "previous runtime")
	assertFile(t, filepath.Join(marker, "keep.txt"), "retained data")
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, marker, bytes.Repeat([]byte("x"), 256))
	if err := next.refresh(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	assertFile(t, app, "updated runtime")
	assertFile(t, marker, next.digest)
}

func TestRuntimeReadsSettingsBesideItsExecutable(t *testing.T) {
	const child = "CREATOR_RUNNER_PATH_TEST"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(child) == "1" {
		settings, err := os.ReadFile(filepath.Join(filepath.Dir(executable), ConfigName))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(string(settings))
		return
	}
	root := t.TempDir()
	runner := openRuntimeFixture(t, executable, filepath.Join(root, "Runtimes"))
	apps := []string{"Personal fixture", "Work fixture"}
	for _, app := range apps {
		path := runtimeDestination(t, root, app)
		if err := runner.install(t.Context(), path); err != nil {
			t.Fatal(err)
		}
		writeRuntimeFixture(t, filepath.Join(filepath.Dir(path), ConfigName), []byte(app))
	}
	if err := os.Remove(runner.shared); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, app := range apps {
		path := filepath.Join(root, app, app+".exe")
		cmd := exec.CommandContext(t.Context(), path, "-test.run=^TestRuntimeReadsSettingsBesideItsExecutable$")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil || !bytes.Contains(output, []byte(app+"\n")) {
			t.Fatalf("%s could not read its own configuration: %v\n%s", app, err, output)
		}
	}
}

func BenchmarkPortableRuntimeInstall(b *testing.B) {
	for _, mode := range []string{"copy", "shared"} {
		b.Run(mode, func(b *testing.B) {
			root := b.TempDir()
			source := filepath.Join(root, "runner")
			writeRuntimeFixture(b, source, bytes.Repeat([]byte{0x3b, 0x98, 0x65, 0xf1}, 2<<20))
			runner := openRuntimeFixture(b, source, filepath.Join(root, "Runtimes"))
			if mode == "copy" {
				runner.link = refuseLink
			} else {
				requireFixtureHardLinks(b, source)
			}
			dest := runtimeDestination(b, root, "seed")
			if err := runner.install(b.Context(), dest); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				dir, err := os.MkdirTemp(root, "app-")
				if err != nil {
					b.Fatal(err)
				}
				if err := runner.install(b.Context(), filepath.Join(dir, "Website")); err != nil {
					b.Fatal(err)
				}
				if err := os.RemoveAll(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPortableRuntimeCheckAfterRestart(b *testing.B) {
	root := b.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	data := bytes.Repeat([]byte{0x3b, 0x98, 0x65, 0xf1}, 5<<19)
	writeRuntimeFixture(b, source, data)
	writeRuntimeFixture(b, source+".sha256", fmt.Appendf(nil, "%x", sha256.Sum256(data)))
	app := runtimeDestination(b, root, "installed")
	if err := openRuntimeFixture(b, source, cache).install(b.Context(), app); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		runner := openRuntimeFixture(b, source, cache)
		if err := runner.refresh(b.Context(), app); err != nil {
			b.Fatal(err)
		}
	}
}

func refuseLink(string, string) error { return errors.ErrUnsupported }

func writeRuntimeFixture(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func openRuntimeFixture(t testing.TB, source, shared string) *runtimeFile {
	t.Helper()
	runner, err := newRuntimeFile(source, shared)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func requireFixtureHardLinks(t testing.TB, path string) {
	t.Helper()
	probe := path + ".probe"
	if err := os.Link(path, probe); err != nil {
		t.Skipf("temporary test directory has no hard links: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
}

func runtimeDestination(t testing.TB, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+".exe")
}

func shareFile(t testing.TB, first, second string) bool {
	t.Helper()
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(a, b)
}
