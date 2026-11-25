package webwindow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bistutu/Creator/internal/fileutil"
)

func TestDownloadsPublishCompletedFilesWithDistinctNames(t *testing.T) {
	dir := t.TempDir()
	d := Downloads{}
	t.Cleanup(d.Close)
	const name = "报告 %.csv"
	target := filepath.Join(dir, name)
	first := stageDownload(t, &d, target, name, "first transfer")
	second := stageDownload(t, &d, target, name, "second transfer")
	if filepath.Dir(first) == filepath.Dir(second) {
		t.Fatal("parallel transfers shared a staging directory")
	}
	assertDownloadAbsent(t, target)
	if err := os.WriteFile(target, []byte("file added during the transfer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if finished, err := d.finish(second, nil); finished != name || err != nil {
		t.Fatalf("publishing the second transfer returned %q, %v", finished, err)
	}
	assertDownloadAbsent(t, filepath.Dir(second))
	assertDownloadBytes(t, first, "first transfer")
	assertDownloadBytes(t, filepath.Join(dir, "报告 % (2).csv"), "second transfer")
	if finished, err := d.finish(first, nil); finished != name || err != nil {
		t.Fatalf("publishing the first transfer returned %q, %v", finished, err)
	}
	assertDownloadAbsent(t, filepath.Dir(first))
	assertDownloadBytes(t, filepath.Join(dir, "报告 % (3).csv"), "first transfer")
	assertDownloadBytes(t, target, "file added during the transfer")
}

func TestDownloadsDiscardFailedTransfersAndCleanOnQuit(t *testing.T) {
	dir := t.TempDir()
	d := Downloads{}
	t.Cleanup(d.Close)
	target := filepath.Join(dir, "report.csv")
	stage := stageDownload(t, &d, target, filepath.Base(target), "partial")
	interruption := errors.New("connection closed")
	if name, err := d.finish(stage, interruption); name != "report.csv" || !errors.Is(err, interruption) {
		t.Fatalf("interruption returned %q, %v", name, err)
	}
	assertDownloadAbsent(t, target)
	assertDownloadAbsent(t, filepath.Dir(stage))
	if _, err := d.finish(stage, nil); err != nil {
		t.Fatalf("a repeated completion returned %v", err)
	}
	stage = stageDownload(t, &d, target, filepath.Base(target), "retry")
	if _, err := d.finish(stage, nil); err != nil {
		t.Fatal(err)
	}
	assertDownloadBytes(t, target, "retry")
	active := stageDownload(t, &d, target, filepath.Base(target), "unfinished")
	d.Close()
	assertDownloadAbsent(t, filepath.Dir(active))
	assertDownloadBytes(t, target, "retry")
}

func TestDownloadsHandleUnavailableFiles(t *testing.T) {
	dir := t.TempDir()
	d := Downloads{}
	t.Cleanup(d.Close)
	interruption := errors.New("connection closed before choosing a destination")
	if _, err := d.finish("", interruption); !errors.Is(err, interruption) {
		t.Fatalf("interruption without a destination returned %v", err)
	}
	if _, err := d.start("", "report.csv"); err == nil {
		t.Fatal("download accepted a missing destination")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.start(file, "report.csv"); err == nil {
		t.Fatal("download accepted a non-directory destination")
	}
	target := filepath.Join(dir, "report.csv")
	stage := stageDownload(t, &d, target, "../outside.csv", "complete")
	if filepath.Base(stage) != "download" {
		t.Fatalf("unsafe suggested name selected %q", stage)
	}
	if err := os.Remove(stage); err != nil {
		t.Fatal(err)
	}
	if name, err := d.finish(stage, nil); name != "download" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing download returned %q, %v", name, err)
	}
	assertDownloadAbsent(t, filepath.Dir(stage))
	assertDownloadAbsent(t, filepath.Join(dir, "download"))
	stage, err := d.start(dir, filepath.Base(target))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if name, err := d.finish(stage, nil); name != "report.csv" || !errors.Is(err, fileutil.ErrNotRegular) {
		t.Fatalf("a nonregular staged download returned %q, %v", name, err)
	}
	assertDownloadAbsent(t, filepath.Dir(stage))
	assertDownloadAbsent(t, target)
}

func stageDownload(t *testing.T, d *Downloads, target, name, contents string) string {
	t.Helper()
	stage, err := d.start(filepath.Dir(target), name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return stage
}

func assertDownloadAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists: %v", path, err)
	}
}

func assertDownloadBytes(t *testing.T, path, want string) {
	t.Helper()
	if contents, err := os.ReadFile(path); err != nil || string(contents) != want {
		t.Fatalf("%s contains %q, want %q (%v)", path, contents, want, err)
	}
}
