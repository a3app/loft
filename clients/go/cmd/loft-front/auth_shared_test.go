package main

// Lab GET/WS nodes use LOFT_TOKEN as the client secret on an empty ring.

import (
	"os"
	"testing"

	"github.com/a3app/loft/internal/transport"
)

func TestMain(m *testing.M) {
	transport.AuthShared = true
	os.Exit(m.Run())
}
