package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// TestNavigationConsistency keeps the signed-in header identical across pages.
// The nav order is part of the visual contract: changing it makes the header
// jump when the user moves between pages. Unified templates are checked per
// deployment mode: ForMode output is what each mode actually serves.
func TestNavigationConsistency(t *testing.T) {
	want := []string{"My usage", "API keys", "Org usage", "My team", "Routing", "Admin"}
	files := []string{"account.html", "dashboard.html", "manager.html", "admin.html", "routing.html"}

	headerRE := regexp.MustCompile(`(?s)<header class="appbar">.*?</header>`)
	navRE := regexp.MustCompile(`(?s)<nav>(.*?)</nav>`)
	itemRE := regexp.MustCompile(`(?s)<(?:a|button)\b[^>]*>(.*?)</(?:a|button)>`)
	tagRE := regexp.MustCompile(`<[^>]+>`)

	for _, file := range files {
		data, err := FS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, standalone := range []bool{false, true} {
			page, err := ForMode(data, standalone)
			if err != nil {
				t.Fatalf("%s standalone=%v: %v", file, standalone, err)
			}
			header := headerRE.Find(page)
			if header == nil {
				t.Fatalf("%s standalone=%v: appbar header not found", file, standalone)
			}
			nav := navRE.FindSubmatch(header)
			if nav == nil {
				t.Fatalf("%s standalone=%v: appbar nav not found", file, standalone)
			}
			var got []string
			for _, m := range itemRE.FindAllSubmatch(nav[1], -1) {
				got = append(got, strings.TrimSpace(tagRE.ReplaceAllString(string(m[1]), "")))
			}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("%s standalone=%v: nav order = %v, want %v", file, standalone, got, want)
			}
			if !strings.Contains(string(page), "max-width:1360px") {
				t.Errorf("%s standalone=%v: appbar missing shared max-width", file, standalone)
			}
			if !strings.Contains(string(page), "min-height:58px") {
				t.Errorf("%s standalone=%v: appbar missing shared height", file, standalone)
			}
		}
	}
}
