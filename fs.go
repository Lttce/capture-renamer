package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileSystem interface {
	ReadDir(name string) ([]os.DirEntry, error)
	Rename(oldPath, newPath string) error
	Stat(name string) (os.FileInfo, error)
	IsNotExist(err error) bool
}

type OSFileSystem struct{}

func (OSFileSystem) ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}

func (OSFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (OSFileSystem) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

func (OSFileSystem) IsNotExist(err error) bool {
	return os.IsNotExist(err)
}

func UniqueNewName(fs FileSystem, base string) string {
	if _, err := fs.Stat(base); fs.IsNotExist(err) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := fs.Stat(name); fs.IsNotExist(err) {
			return name
		}
	}
}
