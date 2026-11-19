package dashboard

import (
	"bytes"
	"testing"
)

// The manager picker lives in a single embedded HTML file, so these are
// source-level guards for the behavior that has no browser test harness in
// this repository. The backend no-key scope guard has its own unit tests.
func TestManagerPickerReviewGuards(t *testing.T) {
	data, err := FS.ReadFile("manager.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"const TEAM_SEARCH_LIMIT=200;",
		"ranked.slice(0,TEAM_SEARCH_LIMIT)",
		"username:r.username||''",
		"no key yet",
		"manager outside this view",
		"PageDown",
		"PageUp",
		"aria-expanded=\"false\"",
		"e.button!==0",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("manager.html no longer contains %q", want)
		}
	}
}

// The team KPI tiles must show spend for the selected window, including the
// people the dashboard country policy hides from the table.
func TestManagerShowsTeamSpendKPI(t *testing.T) {
	data, err := FS.ReadFile("manager.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`id="kpiSpendTile"`,
		`id="kSpendL">Team spend`,
		`id="kSpend"`,
		`document.getElementById('kSpend').textContent=fmt$(spendCost);`,
		`tokens · '+spendReq+' requests this window'`,
		`personView?'Person spend':(adminScope&&!drillSlug?'Org spend':'Team spend')`,
		`document.getElementById('kSpendL').textContent='My spend';`,
		`.demo-private #kpiSpendTile,.demo-private #kpiBudgetTile`,
		// Self view must be self-pinned: /dashboard/* is admin-only and
		// unscoped for admins, so it cannot source "My spend".
		`/api/v1/me/usage/summary?range='+range+'&ref='+encodeURIComponent(savingsRef)`,
		`/api/v1/me/usage/models?range='+range+'&ref='+encodeURIComponent(savingsRef)`,
		`id="tlPanel"`,
		// The Saved KPI needs the reference model on the self path too, and a
		// ref change must not fall back to the org charts (403 for plain users).
		`onchange="refChanged()"`,
		`function refChanged(){`,
		`if(selfMode){`,
		`selfMode=true;`,
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("manager.html no longer contains %q", want)
		}
	}
	if bytes.Contains(data, []byte("/api/v1/dashboard/users")) {
		t.Error("self view must not use the admin-only org-wide /api/v1/dashboard/users")
	}
	// The self path relies on chartJSON, which must be defined on this page
	// (node --check only validates syntax, not undefined references).
	if !bytes.Contains(data, []byte("async function chartJSON(url){")) {
		t.Error("manager.html must define chartJSON for the self view")
	}
	// #chartsError and chartFailure belong to the standalone dashboard page;
	// referencing them here throws at runtime.
	for _, unwanted := range []string{"chartsError", "chartFailure("} {
		if bytes.Contains(data, []byte(unwanted)) {
			t.Errorf("manager.html must not reference %q", unwanted)
		}
	}
}

func TestManagerDemoModeHidesActualSpend(t *testing.T) {
	data, err := FS.ReadFile("manager.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Demo mode · costs hidden",
		".demo-private .demo-cost{display:none!important}",
		"function syncDemoMetrics()",
		`class="num demo-cost" data-s="CostUSD">Spend`,
		`class="num demo-cost">'+fmt$(m.cost_usd)`,
		"if(demoPrivacyEnabled()&&b.dataset.um==='cost')return;",
		"if(demoPrivacyEnabled()&&b.dataset.tm==='cost')return;",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("manager.html no longer contains %q", want)
		}
	}
}

func TestManagerTimelineRanksByUsage(t *testing.T) {
	data, err := FS.ReadFile("manager.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"const ranked=[...totals.entries()].filter(([,v])=>v>0).sort((a,b)=>b[1]-a[1]);",
		"const top=ranked.slice(0,7).map(([n])=>n);",
		"const tail=ranked.slice(7).map(([n])=>n);",
		"tail.length?['__other__']",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("manager timeline no longer contains %q", want)
		}
	}
}
