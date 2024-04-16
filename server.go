package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/unrolled/render"

	"github.com/xbsoftware/wfs"
	local "github.com/xbsoftware/wfs-local"
)

var format = render.New()

var drive wfs.Drive

type FSFeatures struct {
	Preview map[string]bool `json:"preview"`
	Meta    map[string]bool `json:"meta"`
}

var features = FSFeatures{
	Preview: map[string]bool{},
	Meta:    map[string]bool{},
}

var Config AppConfig

func main() {
	flag.StringVar(&Config.Preview, "preview", "", "url of preview generation service")
	flag.BoolVar(&Config.Readonly, "readonly", false, "readonly mode")
	flag.Int64Var(&Config.UploadLimit, "limit", 10_000_000, "max file size to upload")
	flag.StringVar(&Config.Server.Port, "port", ":3200", "port for web server")
	flag.Parse()

	readConfig()

	if Config.Root == "" {
		args := flag.Args()
		if len(args) > 0 {
			Config.Root = args[0]
		} else {
			name, err := os.MkdirTemp("", "fm")
			if err == nil {
				Config.Root = name
			}
		}
	}

	// configure features
	features.Meta["audio"] = true
	features.Meta["image"] = true
	features.Preview["image"] = true
	if Config.Preview != "" {
		features.Preview["document"] = true
		features.Preview["code"] = true
	}

	// common drive access
	var err error
	driveConfig := wfs.DriveConfig{Verbose: true}
	driveConfig.Operation = &wfs.OperationConfig{PreventNameCollision: true}
	if Config.Readonly {
		temp := wfs.Policy(&wfs.ReadOnlyPolicy{})
		driveConfig.Policy = &temp
	}

	drive, err = local.NewLocalDrive(Config.Root, &driveConfig)
	if err != nil {
		log.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	if len(Config.Server.Cors) > 0 {
		c := cors.New(cors.Options{
			AllowedOrigins:   Config.Server.Cors,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Content-Type", "X-CSRF-Token", "X-Requested-With"},
			AllowCredentials: true,
			MaxAge:           300,
		})
		r.Use(c.Handler)
	}

	r.Get("/files", func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("text")
		data, err := drive.List("/", getListConfig(search))

		if err != nil {
			format.Text(w, 500, err.Error())
			return
		}

		err = format.JSON(w, 200, normalizeItems(data))
	})

	r.Get("/files/{path}", func(w http.ResponseWriter, r *http.Request) {
		path, err := url.QueryUnescape(chi.URLParam(r, "path"))
		if err != nil {
			format.Text(w, 500, err.Error())
			return
		}

		search := r.URL.Query().Get("text")
		data, err := drive.List(path, getListConfig(search))

		if err != nil {
			format.Text(w, 500, err.Error())
			return
		}

		err = format.JSON(w, 200, normalizeItems(data))
	})

	r.Put("/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := url.QueryUnescape(chi.URLParam(r, "id"))
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		data := FileUpdate{}
		err = parseForm(w, r, &data)
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		operation := data.Operation
		if operation == "" {
			format.JSON(w, 500, Response{Error: "'operation' parameter must be provided"})
			return
		}

		switch operation {
		case "rename":
			name := data.Name
			if name == "" {
				format.JSON(w, 500, Response{Error: "'name' parameter must be provided"})
				return
			}

			id, err = drive.Move(id, "", name)
			if err != nil {
				format.JSON(w, 500, Response{Error: err.Error()})
				return
			}

		default:
			format.JSON(w, 500, Response{Error: "operation is not supported"})
			return
		}

		info, err := drive.Info(id)
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		format.JSON(w, 200, Response{Result: &Result{ID: info.ID, Name: info.Name}})
	})

	r.Put("/files", func(w http.ResponseWriter, r *http.Request) {
		data := FileUpdate{}
		err = parseForm(w, r, &data)
		if err != nil {
			format.JSON(w, 500, ResponseMulti{Error: err.Error()})
			return
		}

		operation := data.Operation
		ids := data.Ids
		to := data.Target
		if operation == "" || ids == nil || to == "" {
			format.JSON(w, 500, ResponseMulti{Error: "'operation', 'target' and 'ids' parameters must be provided"})
			return
		}

		result := make([]Result, 0)

		switch operation {
		case "move":
			for _, id := range data.Ids {
				id, err = drive.Move(id, to, "")
				if err != nil {
					format.JSON(w, 500, ResponseMulti{Error: err.Error()})
					return
				}

				info, err := drive.Info(id)
				if err != nil {
					format.JSON(w, 500, ResponseMulti{Error: err.Error()})
					return
				}
				result = append(result, Result{ID: info.ID, Name: info.Name})
			}
		case "copy":
			for _, id := range data.Ids {
				id, err = drive.Copy(id, to, "")
				if err != nil {
					format.JSON(w, 500, ResponseMulti{Error: err.Error()})
					return
				}

				info, err := drive.Info(id)
				if err != nil {
					format.JSON(w, 500, ResponseMulti{Error: err.Error()})
					return
				}
				result = append(result, Result{ID: info.ID, Name: info.Name})
			}
		default:
			format.JSON(w, 500, ResponseMulti{Error: "operation is not supported"})
			return
		}

		format.JSON(w, 200, ResponseMulti{Result: result})
	})

	r.Post("/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := url.QueryUnescape(chi.URLParam(r, "id"))
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		data := NewFile{}
		err = parseForm(w, r, &data)
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		name := data.Name
		typ := data.Type
		if name == "" || typ == "" {
			format.JSON(w, 500, Response{Error: "'type' and 'name' parameters must be provided"})
			return
		}

		id, err = drive.Make(id, name, typ == "folder")
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		info, err := drive.Info(id)
		if err != nil {
			format.JSON(w, 500, Response{Error: err.Error()})
			return
		}

		format.JSON(w, 200, Response{Result: &Result{ID: info.ID, Name: info.Name}})
	})

	r.Delete("/files", func(w http.ResponseWriter, r *http.Request) {
		data := FileUpdate{}
		err = parseForm(w, r, &data)
		if err != nil {
			format.JSON(w, 500, ResponseMulti{Error: err.Error()})
			return
		}

		if data.Ids == nil {
			format.JSON(w, 500, ResponseMulti{Error: "IDs are not provided"})
			return
		}

		for _, id := range data.Ids {
			err = drive.Remove(id)
			if err != nil {
				format.JSON(w, 500, ResponseMulti{Error: err.Error()})
				return
			}
		}

		format.JSON(w, 200, ResponseMulti{})
	})

	r.Get("/direct", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			format.Text(w, 500, "id not provided")
			return
		}

		info, err := drive.Info(id)
		if err != nil {
			format.Text(w, 500, "Access denied")
			return
		}

		data, err := drive.Read(id)
		if err != nil {
			format.Text(w, 500, "Access denied")
			return
		}

		disposition := "inline"
		_, ok := r.URL.Query()["download"]
		if ok {
			disposition = "attachment"
		}

		w.Header().Set("Content-Disposition", disposition+"; filename=\""+info.Name+"\"")
		http.ServeContent(w, r, "", time.Now(), data)
	})

	r.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		// buffer for file parsing, this is NOT the max upload size
		var limit = int64(32 << 20) // default is 32MB
		if Config.UploadLimit < limit {
			limit = Config.UploadLimit
		}

		// this one limit max upload size
		r.Body = http.MaxBytesReader(w, r.Body, Config.UploadLimit)
		r.ParseMultipartForm(limit)

		file, handler, err := r.FormFile("file")
		if err != nil {
			format.JSON(w, 500, Response{Error: "The file has not been uploaded"})
			return
		}
		defer file.Close()

		base := r.URL.Query().Get("id")

		filename := r.Form.Get("name")
		if filename == "" {
			filename = handler.Filename
		}

		parts := strings.Split(filename, "/")
		if len(parts) > 1 {
			for _, p := range parts[:len(parts)-1] {
				if !drive.Exists(base + "/" + p) {
					id, err := drive.Make(base, p, true)
					if err != nil {
						format.JSON(w, 500, Response{Error: err.Error()})
						return
					}
					base = id
				} else {
					base = base + "/" + p
				}
			}
		}

		fileID, err := drive.Make(base, parts[len(parts)-1], false)
		if err != nil {
			format.JSON(w, 500, Response{Error: "Access Denied"})
			return
		}

		err = drive.Write(fileID, file)
		if err != nil {
			format.JSON(w, 500, Response{Error: "Access Denied"})
			return
		}

		info, err := drive.Info(fileID)
		if err != nil {
			format.JSON(w, 500, Response{Error: "Access Denied"})
			return
		}
		format.JSON(w, 200, Response{Result: &Result{ID: info.ID, Name: info.Name}})
	})

	r.Get("/info", getInfo)
	r.Get("/info/{id}", getMetaInfo)
	r.Get("/preview", getFilePreview)

	r.Get("/icons/{size}/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		size := chi.URLParam(r, "size")

		http.ServeFile(w, r, getIconURL(name, size))
	})

	log.Printf("Starting webserver at port " + Config.Server.Port)
	http.ListenAndServe(Config.Server.Port, r)
}

func parseForm(w http.ResponseWriter, r *http.Request, o interface{}) error {
	body := http.MaxBytesReader(w, r.Body, 1048576)
	dec := json.NewDecoder(body)
	err := dec.Decode(&o)

	return err
}

func normalizeItems(files []wfs.File) []File {
	out := make([]File, 0)
	for _, file := range files {
		target := File{}
		temp, _ := json.Marshal(file)
		json.Unmarshal(temp, &target)

		if target.Type == "folder" {
			target.Size = nil

			dir, err := drive.List(target.ID, &wfs.ListConfig{
				SubFolders: false,
				Exclude:    func(name string) bool { return strings.HasPrefix(name, ".") },
			})
			if err == nil {
				target.Count = len(dir)
				target.Lazy = target.Count > 0
			}
		} else {
			target.Type = "file"
		}

		t := time.Unix(file.Date, 0).UTC()
		target.Date = t.Format("2006-01-02 15:04:05")

		out = append(out, target)
	}
	return out
}

func getListConfig(search string) *wfs.ListConfig {
	if search == "" {
		return &wfs.ListConfig{
			SubFolders: false,
			Exclude:    func(name string) bool { return strings.HasPrefix(name, ".") },
		}
	} else {
		search = strings.ToLower(search)
		return &wfs.ListConfig{
			SubFolders: true,
			Include:    func(name string) bool { return strings.Contains(strings.ToLower(name), search) },
			Exclude:    func(name string) bool { return strings.HasPrefix(name, ".") },
		}
	}
}

func readConfig() {
	k := koanf.New(".")
	f := file.Provider("config.yml")
	if err := k.Load(f, yaml.Parser()); err != nil {
		log.Println("warning, can't load config from config.yml")
	}

	k.Load(env.Provider("APP_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "APP_")), "_", ".", -1)
	}), nil)

	k.Unmarshal("", &Config)
}

type File struct {
	Count int    `json:"count,omitempty"`
	Lazy  bool   `json:"lazy,omitempty"`
	Size  *int64 `json:"size,omitempty"`
	Date  string `json:"date"`
	wfs.File
}

type FileUpdate struct {
	Operation string
	Name      string
	Target    string
	Ids       []string
}

type NewFile struct {
	Name string
	Type string
}

type Response struct {
	Error  string  `json:"error,omitempty"`
	Result *Result `json:"result,omitempty"`
}

type ResponseMulti struct {
	Error  string   `json:"error,omitempty"`
	Result []Result `json:"result,omitempty"`
}

type Result struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
