// エントリポイント。各コンポーネントの組み立てとstdinループのみ。
// 処理の流れ:
//   1. 状態・ファイルシステム・ウォッチャーを初期化
//   2. 起動時に既存ファイルをスキャン（リネーム対象から除外）
//   3. ウォッチャーを別goroutineで起動（ポーリング監視）
//   4. メインgoroutineでstdin入力を受け付け、prefix切替
//   5. Ctrl+C または stdin 終了で停止
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
)

func main() {
	state := NewState(InitialPrefix)
	fs := OSFileSystem{}
	watcher := NewWatcher(fs, state, SaveFolder)

	if err := watcher.ScanExisting(); err != nil {
		log.Fatalf("scan existing files: %v", err)
	}

	fmt.Printf("Monitoring %s (prefix=%q, interval=%v)\n", SaveFolder, InitialPrefix, PollInterval)
	fmt.Println("Enter new prefix + Enter to switch, Ctrl+C to exit")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	go watcher.Start(PollInterval)

	// stdin から prefix 入力を受け付ける
	scanner := bufio.NewScanner(os.Stdin)
	stdinDone := make(chan struct{})
	go func() {
		for scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}
			state.SetPrefix(input)
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

	watcher.Stop()
}
