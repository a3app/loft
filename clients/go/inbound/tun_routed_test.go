package inbound

import "testing"

func TestStartRoutedTUNNeedsName(t *testing.T) {
	if _, err := StartRoutedTUN("", "10.8.0.2/32", nil); err == nil {
		t.Fatal("empty name")
	}
	if _, err := StartRoutedTUN("auto", "10.8.0.2/32", nil); err == nil {
		t.Fatal("auto")
	}
}
