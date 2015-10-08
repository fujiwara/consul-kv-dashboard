package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
)

type AssetFileSystem struct {
	Prefix string
}

func NewAssetFileSystem(prefix string) AssetFileSystem {
	if prefix == "" {
		prefix = "/"
	}
	return AssetFileSystem{Prefix: prefix}
}

type AssetFile struct {
	*bytes.Reader
	os.FileInfo
}

func (fs AssetFileSystem) Open(name string) (http.File, error) {
	path := name
	path = strings.TrimPrefix(path, fs.Prefix)
	data, err := Asset(path)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	info, _ := AssetInfo(path)
	file := &AssetFile{
		bytes.NewReader(data),
		info,
	}
	return file, nil
}

func (f *AssetFile) Close() error {
	return nil
}

func (f *AssetFile) Readdir(count int) ([]os.FileInfo, error) {
	return []os.FileInfo{}, nil
}

func (f *AssetFile) Stat() (os.FileInfo, error) {
	return f.FileInfo, nil
}
