package webwindow

import (
	"log"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

func UseSystemDownloadsFolder() {
	if directory, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DONT_VERIFY); err == nil && directory != "" {
		mygo.App.SetPath(mygo.PathDownloads, directory)
	}
}

func hideDownloadDirectory(path string) {
	name, err := windows.UTF16PtrFromString(path)
	if err == nil {
		err = windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_HIDDEN)
	}
	if err != nil {
		log.Printf("hide website download staging: %v", err)
	}
}
