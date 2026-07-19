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

	select {
	case <-sig:
		fmt.Println("\nShutting down...")
	case <-stdinDone:
	}

	watcher.Stop()
}
