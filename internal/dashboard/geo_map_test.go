package dashboard

import (
	"strings"
	"testing"
)

func TestGeoMapUsesNightLightGlowStyle(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"var GEO_LAND_COLOR='#0b1522';",
		"var GEO_GLOW_STOPS=['#6d4a21','#c77722','#ff9d2e','#ffe6a3'];",
		"background:linear-gradient(90deg,#182231,#8a5a24,#ff9d2e,#ffe6a3)",
		"d3.interpolateRgbBasis(GEO_GLOW_STOPS)",
		"defs.append('radialGradient').attr('id','geo-glow-grad')",
		"d3.geoGraticule10()",
		"geoGlowRadius(value,max)",
		"mix-blend-mode:screen",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
	for _, old := range []string{"#39d353", "#9be9a8", "GEO_ZERO_COLOR"} {
		if strings.Contains(string(data), old) {
			t.Errorf("dashboard.html still contains old choropleth color %q", old)
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
		"geoHasUSStateData?(geoValueByState.get('US|'+code)||0):(geoValueByCountry.get('US')||0)",
		"US country total; state breakdown unavailable",
		"geoStateGlows.get(code)",
		"geoHasUSStateData){",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}
