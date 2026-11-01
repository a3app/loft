package config

import "testing"

func TestEdgeByte(t *testing.T) {
	plain, err := (&ServerConfig{}).EdgeByte()
	if err != nil || plain != 0 {
		t.Fatalf("empty %d %v", plain, err)
	}
	tight, err := (&ServerConfig{CDN: "yandex"}).EdgeByte()
	if err != nil || tight != 1 {
		t.Fatalf("yandex %d %v", tight, err)
	}
	got, err := (&ServerConfig{Edge: "tight"}).EdgeByte()
	if err != nil || got != 1 {
		t.Fatalf("tight %d %v", got, err)
	}
	got, err = (&ServerConfig{Edge: "plain", CDN: "yandex"}).EdgeByte()
	if err != nil || got != 0 {
		t.Fatalf("explicit plain %d %v", got, err)
	}
	if _, err = (&ServerConfig{Edge: "yandex"}).EdgeByte(); err == nil {
		t.Fatal("vendor name accepted")
	}
}
