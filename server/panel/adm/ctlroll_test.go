package adm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/server/panel/admproto"
)

// TestControllerRoll: a new key, a hand-over signed by the old one, the node
// restarted, and the panel shows the new key and until when clients follow.
func TestControllerRoll(t *testing.T) {
	o, fn, _, _ := setup(t)
	o.Etc = t.TempDir()
	fn.reload = true
	os.WriteFile(filepath.Join(o.Etc, "node.conf"), []byte(nodeConf0), 0o640)
	os.WriteFile(filepath.Join(o.Etc, "loft.env"), []byte("LOFT_TOKEN=test-token-please-change\n"), 0o640)
	if _, err := ecrypto.LoadControllerKey(filepath.Join(o.Dir, "controller.seed")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, _ := o.NodeConfig(ctx)
	if before.KeyRollUntil != 0 {
		t.Fatal("hand-over shown before any")
	}
	if _, err := o.ControllerRoll(ctx, admproto.CtlRoll{Days: 0}); err == nil {
		t.Fatal("zero-day window accepted")
	}
	after, err := o.ControllerRoll(ctx, admproto.CtlRoll{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	if after.ControllerPub == before.ControllerPub || len(after.ControllerPub) != 64 {
		t.Fatalf("key not replaced: %s → %s", before.ControllerPub, after.ControllerPub)
	}
	until := time.Unix(after.KeyRollUntil, 0)
	if until.Before(time.Now().Add(29*24*time.Hour)) || until.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("hand-over until %v", until)
	}
}
