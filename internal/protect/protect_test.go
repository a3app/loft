package protect

import (
	"testing"
	"time"
)

func TestTimeoutWithoutProtector(t *testing.T) {
	Set(nil)
	_ = Bind("")
	d := NetDialer(50 * time.Millisecond)
	if d.Control != nil {
		t.Fatal("control")
	}
}

func TestSetClears(t *testing.T) {
	_ = Bind("")
	Set(func(fd int) bool { return true })
	if NetDialer(time.Second).Control == nil {
		t.Fatal("want control")
	}
	Set(nil)
	if NetDialer(time.Second).Control != nil {
		t.Fatal("cleared")
	}
}
