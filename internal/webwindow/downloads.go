package webwindow

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/fileutil"
)

const (
	downloadStagePrefix  = ".creator-download-"
	downloadWarningTitle = "下载未完成"
)

// Downloads keeps each transfer private until a complete file can be published.
// Its callbacks and Close run on the application's UI thread.
type Downloads struct {
	active    map[string]string
	leftovers []string
	warnings  []downloadWarning
	showing   bool
}

type downloadWarning struct {
	show   func(detail string)
	detail string
}

func (d *Downloads) Bind(window *mygo.Window, page *mygo.Page) {
	show := func(detail string) { showWindowDownloadWarning(window, detail) }
	page.OnWillDownload(func(event *mygo.DownloadEvent) {
		var directory string
		if event.Path != "" {
			directory = filepath.Dir(event.Path)
		}
		staged := d.reserve(directory, event.SuggestedName, show)
		if staged == "" {
			event.PreventDefault()
			return
		}
		event.Path = staged
	})
	page.OnDownloadDone(func(result *mygo.Download) {
		d.complete(result.Path, result.Err, show)
	})
}

func (d *Downloads) reserve(directory, name string, show func(string)) string {
	staged, err := d.start(directory, name)
	if err != nil {
		d.warn(show, name, "无法写入下载目录。", err)
	}
	return staged
}

func (d *Downloads) complete(staged string, transferErr error, show func(string)) {
	name, err := d.finish(staged, transferErr)
	if err == nil {
		return
	}
	if transferErr != nil {
		d.warn(show, name, "文件传输未完成。请重新下载。", err)
	} else {
		d.warn(show, name, "无法保存到下载目录。", err)
	}
}

func (d *Downloads) start(directory, name string) (string, error) {
	if directory == "" {
		return "", errors.New("download directory unavailable")
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." || strings.ContainsRune(name, 0) {
		name = "download"
	}
	folder, err := os.MkdirTemp(directory, downloadStagePrefix)
	if err != nil {
		return "", err
	}
	hideDownloadDirectory(folder)
	staged := filepath.Join(folder, name)
	if d.active == nil {
		d.active = make(map[string]string)
	}
	d.active[staged] = name
	return staged, nil
}

func (d *Downloads) finish(staged string, transferErr error) (string, error) {
	name, known := d.active[staged]
	if !known {
		if staged == "" {
			return "", transferErr
		}
		return "", nil
	}
	delete(d.active, staged)
	folder := filepath.Dir(staged)
	defer d.clean(folder)
	if transferErr != nil {
		return name, transferErr
	}
	info, err := os.Lstat(staged)
	if err != nil {
		return name, err
	}
	if !info.Mode().IsRegular() {
		return name, fileutil.ErrNotRegular
	}
	return name, publishDownload(staged, filepath.Dir(folder), name)
}

func publishDownload(staged, folder, name string) error {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	destination := filepath.Join(folder, name)
	for number := 2; ; number++ {
		err := fileutil.PublishNew(staged, destination)
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		destination = filepath.Join(folder, fmt.Sprintf("%s (%d)%s", base, number, ext))
	}
}

func (d *Downloads) warn(show func(string), name, detail string, err error) {
	log.Printf("website download %q: %v", name, err)
	if name != "" {
		detail = name + "\n" + detail
	}
	d.warnings = append(d.warnings, downloadWarning{show, detail})
	if !d.showing {
		d.showing = true
		go mygo.RunOnMain(d.showWarnings)
	}
}

func (d *Downloads) showWarnings() {
	defer func() { d.showing = false }()
	for len(d.warnings) != 0 {
		warning := d.warnings[0]
		d.warnings[0] = downloadWarning{}
		d.warnings = d.warnings[1:]
		warning.show(warning.detail)
	}
}

func showWindowDownloadWarning(window *mygo.Window, detail string) {
	if !window.IsDestroyed() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent: window, Type: mygo.MessageWarning, Title: downloadWarningTitle,
			Message: downloadWarningTitle, Detail: detail, Buttons: []string{"确定"},
		})
	}
}

func (d *Downloads) clean(folder string) {
	if err := os.RemoveAll(folder); err != nil {
		log.Printf("remove website download staging: %v", err)
		d.leftovers = append(d.leftovers, folder)
	}
}

// Close removes incomplete transfers and retries cleanup of files the engine
// still held when reporting a failure.
func (d *Downloads) Close() {
	for file := range d.active {
		d.clean(filepath.Dir(file))
	}
	clear(d.active)
	leftovers := d.leftovers
	d.leftovers = nil
	for _, folder := range leftovers {
		d.clean(folder)
	}
}
