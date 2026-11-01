package main

// logger_amd64.go defines loggerCallback for 64-bit Windows: wintun.dll calls
// it for each driver log message and it hands the message to logLine
// (main.go). The 32-bit variant lives in logger_386.go because there the
// timestamp argument arrives as two words.

import "syscall"

// loggerCallback is the C-callable function passed to WintunSetLogger.
// level is 0 info, 1 warning, 2 error; ts is the message time, unused here.
var loggerCallback = syscall.NewCallback(func(level, ts uintptr, msg *uint16) uintptr {
	logLine(level, msg)
	return 0
})
