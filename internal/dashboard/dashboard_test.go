package dashboard

import (
	"bytes"
	"testing"
)

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
		"const tooltipCallbacks={label:tooltipLabel,title:tooltipTitle};",
		"userCostChart.options.plugins.tooltip.callbacks=tooltipCallbacks;",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
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
