package dashboard

import (
	"strings"
	"testing"
)

func TestGeoMapShowsZeroUsageAsLightGreen(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"var GEO_ZERO_COLOR='#9be9a8';",
		"if(!(value>0)||!(max>0))return GEO_ZERO_COLOR;",
		"background:linear-gradient(90deg,#9be9a8,#39d353,#d29922,#f85149)",
		"d3.interpolateRgbBasis([GEO_ZERO_COLOR,'#39d353','#d29922','#f85149'])",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}

func TestGeoMapUsesCountryTotalWhenUSStateBreakdownIsMissing(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"geoHasUSStateData=Array.from(byState.keys()).some(function(k){return k.indexOf('US|')===0&&(byState.get(k)||0)>0;});",
		"geoHasUSStateData?(byState.get('US|'+code)||0):(byCountry.get('US')||0)",
		"US country total; state breakdown unavailable",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}
