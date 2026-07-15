// Package checks contains the actual monitor implementations run by agents.
package checks

import "time"

// Result is the outcome of a single check execution.
type Result struct {
	Up      bool
	Latency time.Duration
	Message string
}

func down(msg string) Result { return Result{Up: false, Message: msg} }
func up(latency time.Duration, msg string) Result {
	return Result{Up: true, Latency: latency, Message: msg}
}
