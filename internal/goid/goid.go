// Package goid recognizes the calling goroutine, for the few places that must
// tell whether they are being called from a goroutine they started: something
// that waits for that goroutine to end would otherwise wait for itself.
package goid

import (
	"bytes"
	"runtime"
	"strconv"
)

// Current returns the runtime's number for the calling goroutine, read from the
// header of its stack trace ("goroutine 18 [running]:"). It is only used to
// recognize a goroutine, never to address one.
func Current() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	field := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	if i := bytes.IndexByte(field, ' '); i >= 0 {
		field = field[:i]
	}
	id, _ := strconv.ParseUint(string(field), 10, 64)
	return id
}
