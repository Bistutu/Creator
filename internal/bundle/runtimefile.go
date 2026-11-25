package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Bistutu/Creator/internal/fileutil"
)

const runtimeMarker = ".runtime"

var errBundledRuntimeChanged = errors.New("随附的应用运行程序已更改，请重新打开 Creator")

// runtimeFile shares a portable executable through an independent hard link
// in the application data directory. Runtime updates replace directory entries
// so an existing link continues to have the previous bytes until it is updated.
type runtimeFile struct {
	source string
	shared string
	size   int64
	link   func(string, string) error

	identityMu sync.Mutex
	digest     string // Set once by identify.

	staging  chan struct{}
	verified os.FileInfo // Accessed while holding the staging slot.
}

func newRuntimeFile(source, cacheDir string) (*runtimeFile, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("读取随附的应用运行程序：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("应用运行程序文件无效：%s", source)
	}
	return &runtimeFile{
		source: source, shared: filepath.Join(cacheDir, filepath.Base(source)),
		size: info.Size(), link: os.Link, staging: make(chan struct{}, 1),
	}, nil
}

// install writes an executable and its version in the application's staging
// directory.
func (r *runtimeFile) install(ctx context.Context, path string) error {
	if err := r.identify(ctx); err != nil {
		return err
	}
	return r.stage(ctx, path)
}

// refresh updates only the executable. Configuration, artwork and the
// application's name stay in the existing directory.
func (r *runtimeFile) refresh(ctx context.Context, path string) error {
	if err := r.identify(ctx); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	marker := filepath.Join(dir, runtimeMarker)
	data, err := fileutil.ReadRegular(marker, sha256.Size*2)
	if err == nil && string(data) == r.digest {
		if info, statErr := requiredFile(path, true); statErr == nil && info.Size() == r.size {
			return nil
		}
	}
	if errors.Is(err, fileutil.ErrNotRegular) {
		// A version marker can be atomically replaced even if it is a pipe
		// or link. A directory may contain files and must stay in place.
		if info, statErr := os.Lstat(marker); statErr == nil && !info.IsDir() {
			err = nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fileutil.ErrTooLarge) {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".runner-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	executable := filepath.Join(stage, filepath.Base(path))
	if err := r.stage(ctx, executable); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(executable, path); err != nil {
		return err
	}
	return os.Rename(filepath.Join(stage, runtimeMarker), marker)
}

// Both install paths can retry a failed first read.
func (r *runtimeFile) identify(ctx context.Context) error {
	r.identityMu.Lock()
	defer r.identityMu.Unlock()
	if err := ctx.Err(); err != nil || r.digest != "" {
		return err
	}
	// Release packages carry the digest of their bundled executable. Older
	// packages can still identify it by reading the file on first use.
	if data, err := readBundleResource(r.source+".sha256", sha256.Size*2); err == nil && len(data) == sha256.Size*2 {
		if decoded, err := hex.DecodeString(string(data)); err == nil {
			r.digest = hex.EncodeToString(decoded)
			return nil
		}
	}
	file, err := r.openSource()
	if err != nil {
		return err
	}
	defer file.Close()
	digest, err := hashRuntime(file, r.size)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.digest = digest
	return nil
}

func (r *runtimeFile) openSource() (*os.File, error) {
	file, info, err := fileutil.OpenRegular(r.source)
	if err != nil {
		return nil, err
	}
	if info.Size() != r.size {
		file.Close()
		return nil, errBundledRuntimeChanged
	}
	return file, nil
}

func (r *runtimeFile) stage(ctx context.Context, path string) error {
	// Serialize shared-file preparation. Installed version checks can run
	// while an unrelated executable is being copied or repaired.
	select {
	case r.staging <- struct{}{}:
		defer func() { <-r.staging }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := r.materialize(ctx, path); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(path), runtimeMarker), []byte(r.digest), 0o600)
}

func (r *runtimeFile) materialize(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.linkCurrent(path) {
		return nil
	}
	// In the absence of hard links the app gets its own executable.
	src, err := r.openSource()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, r.size+1))
	if err := errors.Join(err, dst.Close()); err != nil {
		return err
	}
	if written != r.size || hex.EncodeToString(hash.Sum(nil)) != r.digest {
		return errBundledRuntimeChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.remember(path)
	return nil
}

func (r *runtimeFile) linkCurrent(path string) bool {
	if !r.sharedIsCurrent() || r.link(r.shared, path) != nil {
		return false
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() && os.SameFile(r.verified, info) && r.verified.ModTime().Equal(info.ModTime()) {
		return true
	}
	// The shared name may have been replaced while this link was being made.
	_ = os.Remove(path)
	return false
}

func (r *runtimeFile) sharedIsCurrent() bool {
	if link, err := os.Lstat(r.shared); err != nil || !link.Mode().IsRegular() {
		return false
	}
	file, info, err := fileutil.OpenRegular(r.shared)
	if err != nil {
		return false
	}
	defer file.Close()
	// Stat the opened file so the identity belongs to the bytes we hash,
	// including when Windows resolves file IDs lazily for path-based stats.
	if info.Size() != r.size ||
		(runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return false
	}
	if r.verified != nil && os.SameFile(r.verified, info) && r.verified.ModTime().Equal(info.ModTime()) {
		return true
	}
	digest, err := hashRuntime(file, r.size)
	if err != nil || digest != r.digest {
		return false
	}
	r.verified = info
	return true
}

// A cache error leaves the complete standalone executable in place.
func (r *runtimeFile) remember(executable string) {
	dir := filepath.Dir(r.shared)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	stage, err := os.MkdirTemp(dir, ".runner-")
	if err != nil {
		return
	}
	defer os.RemoveAll(stage)
	link := filepath.Join(stage, "runner")
	if r.link(executable, link) != nil {
		return
	}
	file, info, err := fileutil.OpenRegular(link)
	if err != nil {
		return
	}
	if file.Close() == nil && os.Rename(link, r.shared) == nil {
		r.verified = info
	}
}

func hashRuntime(file *os.File, size int64) (string, error) {
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, size+1))
	if err != nil {
		return "", err
	}
	if count != size {
		return "", errBundledRuntimeChanged
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
