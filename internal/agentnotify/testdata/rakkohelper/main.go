// Package main stands in for Rakko.exe in the Windows hook tests: a shim
// runs it with Rakko's arguments and pipes the tool's payload into it, and
// the test reads back exactly what arrived.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	// RAKKO_HELPER_STDIN_FILE writes the bytes the app received, so a test
	// can compare them with what the tool sent without going through a
	// console code page.
	stdin, _ := io.ReadAll(os.Stdin)
	if path := os.Getenv("RAKKO_HELPER_STDIN_FILE"); path != "" {
		_ = os.WriteFile(path, stdin, 0o600)
	}
	if os.Getenv("RAKKO_HELPER_ASCII") != "" {
		// The decision a tool actually reads is ASCII JSON.
		fmt.Println(`{"decision":"allow"}`)
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"args":  os.Args[1:],
		"stdin": string(stdin),
	})
}
