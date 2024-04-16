package main

type ConfigServer struct {
	URL  string
	Port string
	Cors []string
}

type AppConfig struct {
	Server      ConfigServer
	Root        string
	Preview     string
	UploadLimit int64
	Readonly    bool
}
