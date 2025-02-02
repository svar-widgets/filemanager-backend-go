package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/disintegration/imaging"
	"github.com/xbsoftware/wfs"
)

func getIconURL(name string, size string) string {
	var re = regexp.MustCompile(`[^A-Za-z0-9.]`)
	name = re.ReplaceAllString(name, "")

	test := "icons/" + size + "/" + name
	_, err := os.Stat(test)
	if !os.IsNotExist(err) {
		return test
	}

	return "icons/" + size + "/file.svg"
}

func serveIconPreview(w http.ResponseWriter, r *http.Request, info *wfs.File) {
	var name string
	if info != nil {
		name = filepath.Ext(info.Name)[1:]
	} else {
		name = "unavailable"
	}
	http.ServeFile(w, r, getIconURL(name+".svg", "big"))
}

func getFilePreview(w http.ResponseWriter, r *http.Request) {
	if Config.Preview == "none" {
		format.Text(w, 500, "Previews not configured")
		return
	}

	id := r.URL.Query().Get("id")
	widthStr := r.URL.Query().Get("width")
	heightStr := r.URL.Query().Get("height")
	width, err := strconv.Atoi(widthStr)
	if err != nil {
		width = 214
	}
	height, err := strconv.Atoi(heightStr)
	if err != nil {
		height = 163
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
	if err != nil {
		serveIconPreview(w, r, nil)
		return
	}

	if info.Size > 50*1000*1000 || width > 2000 || height > 2000 {
		// file is too large, still it is a valid use-case so return some image
		serveIconPreview(w, r, &info)
		return
	}

	source := filepath.Join(Config.Root, id)
	name := filepath.Base(source)
	folder := filepath.Join(Config.Root, id[:len(id)-len(name)], ".preview")
	preview := filepath.Join(folder, name+"___"+widthStr+"x"+heightStr)

	// check previously generated preview
	ext := ".jpg"
	ps, err := os.Stat(preview + ext)
	if err != nil {
		ext = ".png"
		ps, err = os.Stat(preview + ext)
	}
	if err == nil {
		if ps.Size() == 0 {
			// there is a preview placeholder, which means preview can't be generated for this file
			serveIconPreview(w, r, &info)
			return
		} else {
			http.ServeFile(w, r, preview+ext)
		}
		return
	}

	// ensure that preview folder does exist
	os.Mkdir(folder, 0777)

	if Config.Preview != "" {
		file, _ := drive.Read(id)
		if x, ok := file.(io.Closer); ok {
			defer x.Close()
		}
		ext, err = getExternalPreview(file, preview, name, widthStr, heightStr)
	} else {
		if info.Type == "image" {
			ext, err = getImagePreview(source, width, height, preview)
		}
	}

	if err != nil {
		log.Print(err.Error())
		os.WriteFile(preview+".jpg", []byte{}, 0664)
		serveIconPreview(w, r, &info)
		return
	}
	http.ServeFile(w, r, preview+ext)
}

func getImagePreview(source string, width, height int, preview string) (string, error) {
	src, err := imaging.Open(source)
	if err != nil {
		return "", err
	}

	dst := imaging.Thumbnail(src, width, height, imaging.Lanczos)
	err = imaging.Save(dst, preview+".jpg")

	if err != nil {
		return "", err
	}
	return ".jpg", nil
}

func getExternalPreview(file io.ReadSeeker, preview, name, width, height string) (string, error) {
	body, writer := io.Pipe()
	defer body.Close()

	form := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()
		defer form.Close()

		fw, err := form.CreateFormField("width")
		if err != nil {
			log.Println(err.Error())
			return
		}
		io.Copy(fw, bytes.NewBufferString(width))

		fw, err = form.CreateFormField("height")
		if err != nil {
			log.Println(err.Error())
			return
		}
		io.Copy(fw, bytes.NewBufferString(height))

		fw, err = form.CreateFormField("name")
		if err != nil {
			log.Println(err.Error())
			return
		}
		io.Copy(fw, bytes.NewBufferString(name))

		fw, err = form.CreateFormFile("file", name)
		if err != nil {
			log.Println(err.Error())
			return
		}
		io.Copy(fw, file)
	}()

	req, err := http.NewRequest(http.MethodPost, Config.Preview, body)
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", form.FormDataContentType())

	client := &http.Client{}
	res, err := client.Do(req)

	if err != nil {
		return "", fmt.Errorf("preview service %w", err)
	}
	if res.StatusCode != 200 {
		return "", fmt.Errorf("preview service %d", res.StatusCode)
	}
	ext := ".jpg"
	if res.Header.Get("Content-type") == "image/png" {
		ext = ".png"
	}

	defer res.Body.Close()
	fw, err := os.Create(preview + ext)
	if err != nil {
		return "", err
	}
	defer fw.Close()
	_, err = io.Copy(fw, res.Body)

	return ext, err
}
