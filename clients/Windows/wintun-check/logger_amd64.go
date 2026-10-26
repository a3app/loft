package main

import "syscall"

var loggerCallback = syscall.NewCallback(func(level, ts uintptr, msg *uint16) uintptr {
	logLine(level, msg)
	return 0
})
