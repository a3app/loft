package main

import "syscall"

// On 32-bit Windows the 64-bit timestamp arrives as two words.
var loggerCallback = syscall.NewCallback(func(level, tsLo, tsHi uintptr, msg *uint16) uintptr {
	logLine(level, msg)
	return 0
})
