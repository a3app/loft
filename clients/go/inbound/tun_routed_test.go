package inbound

import "testing"

// TestStartRoutedTUNNeedsName refuses an empty name and "auto".
// Either would open a desktop utun; Keenetic must name opkgtunN itself.
// The test does not open a device.
func TestStartRoutedTUNNeedsName(t *testing.T) {
	if _, err := StartRoutedTUN("", "10.8.0.2/32", nil); err == nil {
		t.Fatal("empty name")
	}
	if _, err := StartRoutedTUN("auto", "10.8.0.2/32", nil); err == nil {
		t.Fatal("auto")
	}
}
