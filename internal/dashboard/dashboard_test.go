package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolPieShowsTopNinePlusOther(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("function(d){return d.tool;},9)")) {
		t.Error("tool pie no longer limits to the top 9 tools plus an Other row")
	}
}

func TestDashboardToolMetricToggle(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"let modelMetric='tokens', userPieMetric='cost', toolMetric='tokens';",
		"id=\"tool-metric-toggle\"",
		"<span id=\"tool-metric-label\">Tokens</span>",
		"metric==='users'?(d.users||0)",
		"metric==='users'?' users'",
		"metric==='users'?'':' ('",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}

func TestDashboardEscapesUserControlledIdentityFields(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pmEsc(nameByUser[n])",
		"${pmEsc(r.model||'—')}",
		"${pmEsc(r.provider||'—')}",
		"var name=pmEsc(m.model);",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer escapes user-controlled field with %q", want)
		}
	}
}

func TestTopUsersRankShowsCountryCostCenterAndDemoPrivacy(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"function userCostCenter(tags)",
		"var country=item.tags&&item.tags.country;",
		"var costCenter=userCostCenter(item.tags);",
		`countryFlag(country)+'<span class="ur-name demo-pii">'`,
		`<span class="ur-sub demo-pii">`,
		`<span class="ur-cc">`,
		`var title=demoPrivacyEnabled()?'':' title="'`,
		// The user pie (and its tooltip) is gone: renderUserCostChart renders
		// only the ranked legend, with no unreachable chart code after it.
		"renderUserRank(data,userPieMetric);\n}",
		"<div class=\"ur-grid\" data-cols=\"",
		"grid-auto-flow:column",
		"grid-template-rows:repeat(10,minmax(0,auto))",
		"border-left:1px dotted",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}

// A filtered roster shorter than 21 entries must not leave empty tracks or
// stray rules: the renderer derives the column count from the entry count.
func TestTopUsersRankUsesOnlyTheColumnsTheListNeeds(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`var cols=Math.min(3,Math.max(1,Math.ceil(data.length/10)));`,
		`<div class="ur-grid" data-cols="'+cols+'">`,
		`.ur-grid[data-cols="1"]{grid-template-columns:minmax(0,1fr)}`,
		`.ur-grid[data-cols="1"]::before,.ur-grid[data-cols="1"]::after{display:none}`,
		`.ur-grid[data-cols="2"]{grid-template-columns:repeat(2,minmax(0,1fr))}`,
		`.ur-grid[data-cols="2"]::before{left:50%}`,
		`.ur-grid[data-cols="2"]::after{display:none}`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
	// The data-cols rules must precede the narrow-screen fallback so the
	// fallback still wins at equal specificity.
	cols := strings.Index(page, `.ur-grid[data-cols="1"]`)
	narrow := strings.Index(page, "@media(max-width:900px)")
	if cols < 0 || narrow < 0 || cols > narrow {
		t.Error("data-cols rules must be declared before the max-width:900px fallback")
	}
}

func TestTopUsersRankUsesColumnMajorOrderingOnNarrowScreens(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, ".user-rank .ur-grid{grid-auto-flow:row;grid-template-columns:1fr;grid-template-rows:none}") {
		t.Error("top-user ranking should collapse to one row-major column on narrow screens")
	}
	if !strings.Contains(page, ".user-rank .ur-grid::before,.user-rank .ur-grid::after{display:none}") {
		t.Error("column separators should be hidden on narrow screens")
	}
}

// Yossi's review flagged two stored-XSS sinks reachable from OIDC profile data:
// esc() in admin.html was text-safe but not attribute-safe, and the pie legend's
// local html() helper left double quotes intact inside a title="..." attribute.
func TestDashboardPieLegendTitleEscapesQuotes(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`function pmEsc(s){return String(s==null?'':s).replace(/[&<>"]/g,`,
		"var text=pmEsc(label);",
		"var title=redactName?'':' title=\"'+text+'\"';",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer escapes pie legend titles with %q", want)
		}
	}
	// The removed local helper escaped only &, < and > and was unsafe inside
	// title="...".
	if bytes.Contains(data, []byte("function html(s){return String(s??'').replace(/&/g,'&amp;')")) {
		t.Error("pie legend still uses the unquoted local html() helper")
	}
}

func TestAdminEscapesAttributesAndInlineHandlerStrings(t *testing.T) {
	data, err := FS.ReadFile("admin.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// esc() must escape both quote characters so value="..." and data-* are safe.
		`'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'`,
		"function jsq(s)",
		"jsq(p.name)",
		"jsq(name)",
		"jsq(u)",
		"jsq(p.slug)",
		"value=\"' + esc(p.first_name || '') + '\"",
		"value=\"' + esc(p.last_name || '') + '\"",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("admin.html no longer contains %q", want)
		}
	}
	// A textContent/innerHTML round-trip does not escape quotes.
	if bytes.Contains(data, []byte("escDiv.textContent = String(s);")) {
		t.Error("admin.html esc() regressed to the quote-unsafe textContent helper")
	}
	// User-controlled values must not be spliced into inline JS string literals
	// through the HTML-only encoder.
	for _, bad := range []string{
		"approvePendingUser('\\'' + esc(p.name)",
		"removeFromGroup('\\'' + esc(name)",
		"addToGroup('\\'' + esc(name)",
		"openInviteFor('\\'' + esc(p.slug)",
		"setPersonField('\\'' + esc(p.slug)",
	} {
		if bytes.Contains(data, []byte(bad)) {
			t.Errorf("admin.html still interpolates %q into an inline handler", bad)
		}
	}
}

// jsq() is splices into inline JS string literals inside HTML attributes, so it
// must neutralise the entity opener as well as quotes: the HTML parser would
// otherwise decode "&#39;" back into a quote before JavaScript runs.
func TestAdminJsqEncodingIsEntityAndQuoteSafe(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; jsq behaviour is exercised in CI where node exists")
	}
	data, err := FS.ReadFile("admin.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "function jsq(s)")
	if start < 0 {
		t.Fatal("admin.html no longer defines jsq()")
	}
	depth, end := 0, -1
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatal("could not extract jsq() body")
	}
	fn := src[start:end]

	cases := []struct{ in, want string }{
		{`&#39;);alert(document.domain);//`, `\x26#39;);alert(document.domain);//`},
		{`it's "quoted" & <tag>`, `it\x27s \x22quoted\x22 \x26 \x3ctag>`},
		{`a\b`, `a\x5cb`},
		{"line\nbreak", `line\x0abreak`},
	}
	for _, tc := range cases {
		lit, err := json.Marshal(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("%s;process.stdout.write(jsq(%s));", fn, lit)
		out, err := exec.Command(nodePath, "-e", script).Output()
		if err != nil {
			t.Fatalf("node failed: %v", err)
		}
		if string(out) != tc.want {
			t.Errorf("jsq(%q) = %q, want %q", tc.in, out, tc.want)
		}
		for _, raw := range []string{"&", "'", "\"", "<"} {
			if strings.Contains(string(out), raw) {
				t.Errorf("jsq(%q) left raw %q in the encoded output %q", tc.in, raw, out)
			}
		}
	}
}

// The cost-center legend must name both the directory code and its human
// label, with the share of spend first.
func TestCostCenterChartShowsPercentAndDirectoryCode(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"function costCenterSliceMeta(d,total)",
		"String(d.item.cost_center||'').trim()",
		"function costCenterSliceLabel(meta)",
		"meta.pct+'% — '+id+meta.desc",
		"const share=tokensOnly?'% of tokens':'% spent';",
		"meta.pct+share+' — '",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}

// Demo mode must be safe to screen-share: names are blurred, and every
// actual-spend surface either switches to tokens or disappears. Savings stay
// visible because they are the demo's point.
func TestDemoModeHidesActualSpend(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Demo mode · costs hidden",
		".demo-private .demo-cost{display:none!important}",
		"function syncDemoMetrics()",
		"const tokensOnly=demoPrivacyEnabled();",
		"tokensOnly?function(d){return d.total_tokens||0;}:function(d){return d.cost_usd||0;}",
		"document.getElementById('kpi-cost').textContent=demoPrivacyEnabled()?'Hidden':fmtC(d.total_cost_usd);",
		"cachedRecent=Array.isArray(data)?data:[];",
		`class="num demo-cost">${fmtC(r.cost_usd)}`,
		"if(demoPrivacyEnabled()&&btn.dataset.um==='cost')return;",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}

// Structural string checks cannot catch a misplaced brace: one syntax error
// makes the browser discard the entire inline script, so the dashboard renders
// its skeletons but never issues a single API request. Parse every inline
// script with the same engine the page runs in.
func TestDashboardInlineScriptsParse(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; inline dashboard scripts are parsed in CI where node exists")
	}
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	checked := 0
	for {
		start := strings.Index(src, "<script")
		if start < 0 {
			break
		}
		openEnd := strings.Index(src[start:], ">")
		if openEnd < 0 {
			t.Fatal("unterminated <script> tag")
		}
		openEnd += start
		closeRel := strings.Index(src[openEnd+1:], "</script>")
		if closeRel < 0 {
			t.Fatal("unterminated inline <script> block")
		}
		body := src[openEnd+1 : openEnd+1+closeRel]
		src = src[openEnd+1+closeRel+len("</script>"):]
		if strings.TrimSpace(body) == "" {
			continue
		}
		path := filepath.Join(t.TempDir(), fmt.Sprintf("inline-%d.js", checked))
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(nodePath, "--check", path).CombinedOutput(); err != nil {
			t.Fatalf("inline script %d does not parse: %v\n%s", checked, err, out)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("dashboard.html has no inline scripts to parse")
	}
}
