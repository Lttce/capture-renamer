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
	"strconv"
	"strings"
	"time"
)

func main() {
	folder := flag.String("folder", ".", "監視フォルダのパス")
	prefix := flag.String("prefix", "test", "初期prefix")
	interval := flag.Int("interval", 300, "ポーリング間隔(ms)")
	flag.Parse()

	// 100ms未満のポーリング間隔はビジーループ相当の負荷になるため禁止
	if *interval < 100 {
		log.Fatalf("interval must be at least 100ms, got %dms", *interval)
	}

	if err := validatePrefix(*prefix); err != nil {
		log.Fatalf("invalid -prefix: %v", err)
	}

	w := NewWatcher(*folder, *prefix)

	// 起動時スキャン: 既存ファイルをリネーム対象から除外する
	if err := w.ScanExisting(); err != nil {
		log.Fatalf("scan existing files: %v", err)
	}

	dur := time.Duration(*interval) * time.Millisecond
	fmt.Printf("Monitoring %s (prefix=%q, interval=%v)\n", *folder, *prefix, dur)
	fmt.Println("Enter new prefix to switch, or :seq N to set counter. Ctrl+C to exit")

	// Ctrl+C のハンドリング
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	// Watcher を別 goroutine で起動（ポーリング監視ループ）
	go w.Start(dur)

	// stdin から入力を受け付ける goroutine
	// コマンドは map でディスパッチ。新しいコマンドはここにハンドラを追加する。
	commands := map[string]func([]string){
		"seq": func(args []string) {
			if len(args) != 1 {
				fmt.Println("usage: :seq <N>")
				return
			}
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 1 {
				fmt.Println("counter must be a positive integer")
				return
			}
			w.SetCounter(n)
			fmt.Printf("Counter set to %d\n", n)
		},
	}
	printAvailable := func() {
		fmt.Print("Available commands:")
		for name := range commands {
			fmt.Printf(" :%s", name)
		}
		fmt.Println()
	}
	scanner := bufio.NewScanner(os.Stdin)
	stdinDone := make(chan struct{})
	go func() {
		for scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}
			if strings.HasPrefix(input, ":") {
				parts := strings.Fields(input[1:])
				if len(parts) == 0 {
					printAvailable()
					continue
				}
				handler, ok := commands[parts[0]]
				if !ok {
					printAvailable()
					continue
				}
				handler(parts[1:])
				continue
			}
			if err := validatePrefix(input); err != nil {
				fmt.Println(err)
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
