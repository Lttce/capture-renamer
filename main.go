// エントリポイント。Watcher の初期化と stdin ループのみ。
// 処理の流れ:
//   1. Watcher を初期化し、起動時に既存ファイルをスキャン
//   2. ウォッチャーを別 goroutine で起動（ポーリング監視）
//   3. メイン goroutine で stdin 入力を受け付け、prefix 切替
//   4. Ctrl+C または stdin 終了で停止
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	folder := flag.String("folder", ".", "監視フォルダのパス")
	prefix := flag.String("prefix", "item", "初期prefix")
	interval := flag.Int("interval", 300, "ポーリング間隔(ms)")
	flag.Parse()

	w := NewWatcher(*folder, *prefix)

	// 起動時スキャン: 既存ファイルをリネーム対象から除外する
	if err := w.ScanExisting(); err != nil {
		log.Fatalf("scan existing files: %v", err)
	}

	dur := time.Duration(*interval) * time.Millisecond
	fmt.Printf("Monitoring %s (prefix=%q, interval=%v)\n", *folder, *prefix, dur)
	fmt.Println("Enter new prefix + Enter to switch, Ctrl+C to exit")

	// Ctrl+C のハンドリング
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	// Watcher を別 goroutine で起動（ポーリング監視ループ）
	go w.Start(dur)

	// stdin から prefix 入力を受け付ける goroutine
	scanner := bufio.NewScanner(os.Stdin)
	stdinDone := make(chan struct{})
	go func() {
		for scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}
			w.SetPrefix(input)
			fmt.Printf("Prefix changed to %q, counter reset\n", input)
		}
		close(stdinDone)
	}()

	// Ctrl+C か stdin 終了のどちらかで停止
	select {
	case <-sig:
		fmt.Println("\nShutting down...")
	case <-stdinDone:
	}

	w.Stop()
}
