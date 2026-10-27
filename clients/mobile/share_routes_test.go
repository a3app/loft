package mobile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRoutesURL(t *testing.T) {
	doc := `{"name":"RU direct","dns":"77.88.8.8","serial":20261027,"src":"op","zones":[{"id":"local","name":"Local","builtin":true}],"services":[{"id":"ru","name":"Russia","zone":"local","src":"op","cidrs":["5.255.255.0/24"],"domains":["yandex.ru"]}]}`
	link := EncodeRoutes(doc)
	if !strings.HasPrefix(link, "loft://routes/") {
		t.Fatal(link)
	}
	got, err := ParseRoutes(link)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err := json.Unmarshal([]byte(doc), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &b); err != nil {
		t.Fatal(err)
	}
	if b["name"] != "RU direct" || b["src"] != "op" {
		t.Fatalf("%v", b)
	}
	if _, err := ParseShare(link); err == nil {
		t.Fatal("routes accepted as connection")
	}
}

func TestParseRoutesYAML(t *testing.T) {
	y := "kind: routes\nname: RU direct\nsrc: op\nzones:\n  - id: local\n    name: Local\n    builtin: true\nservices:\n  - id: ru\n    name: Russia\n    zone: local\n    src: op\n    cidrs: [\"5.255.255.0/24\"]\n    domains: [\"yandex.ru\"]\n"
	got, err := ParseRoutes(y)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(got), &d); err != nil {
		t.Fatal(err)
	}
	if d["name"] != "RU direct" {
		t.Fatalf("%v", d)
	}
	if _, err := ParseShare(y); err == nil {
		t.Fatal("kind routes accepted as connection")
	}
}

func TestParseRoutesJSON(t *testing.T) {
	doc := `{"zones":[{"id":"local"}],"services":[{"id":"ru"}]}`
	got, err := ParseRoutes(doc)
	if err != nil || got != doc {
		t.Fatalf("%q %v", got, err)
	}
}
