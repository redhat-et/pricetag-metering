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
