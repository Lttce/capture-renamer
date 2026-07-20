// FileSystem interface とその実装。
// interface で抽象化することで、watcher の単体テストでは MockFS と差し替え可能。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileSystem は OS のファイル操作を抽象化する。
// watcher はこの interface を通してのみファイル操作を行う。
type FileSystem interface {
	ReadDir(name string) ([]os.DirEntry, error)
	Rename(oldPath, newPath string) error
	Stat(name string) (os.FileInfo, error)
	IsNotExist(err error) bool
}

// OSFileSystem は FileSystem の本番実装。生の os パッケージを呼ぶ。
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

// UniqueNewName は base が既に存在する場合、末尾に (1), (2), ... を付与して
// 衝突しないファイル名を返す。存在しなければ base をそのまま返す。
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
