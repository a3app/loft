package web

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func loadDict(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, `"`) {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal([]byte("{"+strings.TrimSuffix(ln, ",")+"}"), &m); err != nil {
			t.Fatalf("%s line %q: %v", path, ln, err)
		}
		for k, v := range m {
			have[k] = v
		}
	}
	return have
}

func russianOnThePage(t *testing.T) []string {
	t.Helper()
	comments := []*regexp.Regexp{regexp.MustCompile(`(?s)/\*.*?\*/`), regexp.MustCompile(`(?s)<!--.*?-->`), regexp.MustCompile(`(?m)(^|[\s;{}])//[^\n]*`)}
	seg := regexp.MustCompile("[^`'\"<>{}\\n\\\\]*[А-Яа-яЁё][^`'\"<>{}\\n\\\\]*")
	lead := regexp.MustCompile(`^[\s,;:()=+?|&!\[\]]+`)
	trail := regexp.MustCompile(`[\s,;:(=+?|&!\[]+$`)
	cyr := regexp.MustCompile(`[А-Яа-яЁё]`)
	var out []string
	seen := map[string]bool{}
	for _, p := range []string{"../design/index.html", "ui/live.js"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		s = comments[0].ReplaceAllString(s, "")
		s = comments[1].ReplaceAllString(s, "")
		s = comments[2].ReplaceAllString(s, "$1")
		for _, m := range seg.FindAllString(s, -1) {
			x := strings.TrimSpace(trail.ReplaceAllString(strings.TrimSpace(lead.ReplaceAllString(strings.TrimSpace(m), "")), ""))
			if x != "" && cyr.MatchString(x) && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out
}

func missingFrom(have map[string]string, need []string) []string {
	var miss []string
	for _, x := range need {
		if _, ok := have[x]; !ok {
			miss = append(miss, x)
		}
	}
	return miss
}

// Every Russian string of the page needs an English entry in en.js: the
// same pieces design/i18n.js swaps at run time.
func TestEnglishCoversThePage(t *testing.T) {
	have := loadDict(t, "../design/en.js")
	if miss := missingFrom(have, russianOnThePage(t)); len(miss) > 0 {
		t.Fatalf("%d Russian strings without English in design/en.js, e.g. %q", len(miss), miss[:min(len(miss), 5)])
	}
}

// zh.js maps the same Russian keys as en.js, so the Chinese pack cannot lag.
func TestChineseCoversThePage(t *testing.T) {
	en := loadDict(t, "../design/en.js")
	zh := loadDict(t, "../design/zh.js")
	if miss := missingFrom(zh, russianOnThePage(t)); len(miss) > 0 {
		t.Fatalf("%d Russian strings without Chinese in design/zh.js, e.g. %q", len(miss), miss[:min(len(miss), 5)])
	}
	var miss []string
	for k := range en {
		if _, ok := zh[k]; !ok {
			miss = append(miss, k)
		}
	}
	if len(miss) > 0 {
		t.Fatalf("%d en.js keys missing from zh.js, e.g. %q", len(miss), miss[:min(len(miss), 5)])
	}
}
