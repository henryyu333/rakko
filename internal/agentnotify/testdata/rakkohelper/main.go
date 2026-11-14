// Package main stands in for Rakko.exe in the Windows hook tests: a shim
// runs it with Rakko's arguments and pipes the tool's payload into it, and
// the test reads back exactly what arrived.
package main

import (
	"encoding/json"
	"io"
	"os"
)

func main() {
	var stdin []byte
	if len(os.Args) > 1 && (os.Args[1] == "--agent-hook" || os.Args[1] == "--agent-approve") {
		stdin, _ = io.ReadAll(os.Stdin)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"args":  os.Args[1:],
		"stdin": string(stdin),
	})
}
