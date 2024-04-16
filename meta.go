package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dhowden/tag"
	"github.com/go-chi/chi"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	"github.com/xbsoftware/wfs"
)

type MusicMeta struct {
	Title  string
	Artist string
	Album  string
	Year   string
	Genre  string
}

type FolderInfo struct {
	Size  int64
	Count int
}

func getMetaInfo(w http.ResponseWriter, r *http.Request) {
	id, err := url.QueryUnescape(chi.URLParam(r, "id"))
	if err != nil {
		format.JSON(w, 500, Response{Error: "id not provided"})
	}

	var info wfs.File
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err = drive.Info(id)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// in case something really went wrong
	if err != nil {
		format.JSON(w, 500, Response{Error: "Access denied"})
		return
	}

	var meta interface{}
	if info.Type == "audio" {
		meta, err = getMusicMetaInfo(id)
	} else if info.Type == "image" {
		meta, err = getImageMetaInfo(id)
	} else if info.Type == "folder" {
		meta, err = getFolderInfo(id)
	} else {
		meta = nil
	}

	if err != nil {
		format.JSON(w, 500, Response{Error: err.Error()})
	} else {
		format.JSON(w, 200, meta)
	}
}

func getMusicMetaInfo(id string) (MusicMeta, error) {
	content, err := drive.Read(id)
	if err != nil {
		return MusicMeta{}, err
	}

	md, err := tag.ReadFrom(content)
	if err != nil {
		return MusicMeta{}, err
	}

	return MusicMeta{
		Title:  md.Title(),
		Artist: md.Artist(),
		Album:  md.Album(),
		Year:   strconv.Itoa(md.Year()),
		Genre:  md.Genre(),
	}, nil
}

func getImageMetaInfo(id string) (map[exif.FieldName]string, error) {
	data, err := drive.Read(id)
	if err != nil {
		return nil, err
	}

	exifmap := make(map[exif.FieldName]string)
	x, err := exif.Decode(data)
	if err == nil || !exif.IsCriticalError(err) {
		x.Walk(walkFunc(func(name exif.FieldName, tag *tiff.Tag) error {
			exifmap[name] = tag.String()
			return nil
		}))
	}

	return exifmap, nil
}

func getFolderInfo(id string) (FolderInfo, error) {
	var size int64
	size, count, err := checkoutDir(id, &size)
	if err != nil {
		return FolderInfo{}, err
	}

	return FolderInfo{
		Count: count,
		Size:  size,
	}, nil
}

func checkoutDir(id string, total *int64) (int64, int, error) {
	dir, err := drive.List(id, &wfs.ListConfig{
		SubFolders: false,
		Exclude:    func(name string) bool { return strings.HasPrefix(name, ".") },
	})
	if err != nil {
		return 0, 0, err
	}

	for _, f := range dir {
		if f.Type == "folder" {
			t, _, err := checkoutDir(f.ID, total)
			if err != nil {
				return 0, 0, err
			}
			*total += t
		} else {
			*total += f.Size
		}
	}
	return *total, len(dir), nil
}

type walkFunc func(exif.FieldName, *tiff.Tag) error

func (f walkFunc) Walk(name exif.FieldName, tag *tiff.Tag) error {
	return f(name, tag)
}
