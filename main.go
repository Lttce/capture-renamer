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
	w := NewWatcher(SaveFolder, InitialPrefix)

	if err := w.ScanExisting(); err != nil {
		log.Fatalf("scan existing files: %v", err)
	}

	fmt.Printf("Monitoring %s (prefix=%q, interval=%v)\n", SaveFolder, InitialPrefix, PollInterval)
	fmt.Println("Enter new prefix + Enter to switch, Ctrl+C to exit")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	go w.Start(PollInterval)

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

	select {
	case <-sig:
		fmt.Println("\nShutting down...")
	case <-stdinDone:
	}

	w.Stop()
}
