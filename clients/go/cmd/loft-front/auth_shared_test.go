package main

// Lab GET/WS nodes use LOFT_TOKEN as the client secret on an empty ring and
// reach echo servers on 127.0.0.1.

import (
	"os"
	"testing"

	"github.com/a3app/loft/internal/transport"
)

func TestMain(m *testing.M) {
	transport.AuthShared = true
	transport.LoopbackOpen = true
	os.Exit(m.Run())
}
