package assets

import (
	"embed"
	"io/fs"
)

var (
	//go:embed web/templates/*.gohtml
	templateFiles embed.FS

	//go:embed web/static/*
	staticFiles embed.FS

	//go:embed schema.sql
	schemaSQL string
)

func Templates() fs.FS {
	return templateFiles
}

func Static() (fs.FS, error) {
	return fs.Sub(staticFiles, "web/static")
}

func Schema() string {
	return schemaSQL
}
