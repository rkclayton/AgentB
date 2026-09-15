package main

import (
	"encoding/json"
	"fmt"
	"os"

	"harness/internal/events"
	"harness/internal/progress"
)

func main() {
	var stream []events.Event
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&stream); err != nil {
		fmt.Fprintln(os.Stderr, "decode event stream:", err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(progress.EvaluateEvents(stream, progress.ArmedSet(false))); err != nil {
		fmt.Fprintln(os.Stderr, "encode detector records:", err)
		os.Exit(2)
	}
}
