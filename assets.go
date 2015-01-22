package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
)

type AssetFileSystem struct {
}

type AssetFile struct {
	*bytes.Reader
	os.FileInfo
}

func (fs AssetFileSystem) Open(name string) (http.File, error) {
	path := name
	path = strings.TrimLeft(path, "/")
	log.Println("open asset", path)
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
	log.Printf("%#v", file)
	return file, nil
}

func (f *AssetFile) Close() error {
	return nil
}

func (f *AssetFile) Readdir(count int) ([]os.FileInfo, error) {
	return []os.FileInfo{}, nil
}

func (f *AssetFile) Stat() (os.FileInfo, error) {
	return f, nil
}
