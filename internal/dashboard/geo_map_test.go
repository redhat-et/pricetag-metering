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
		"var GEO_LAND_COLOR='#0b1221';",
		"var GEO_GLOW_STOPS=['#4b1f87','#8b2cff','#e62cff','#ff77d9','#ffe0f8'];",
		"background:linear-gradient(90deg,#171536,#6425ad,#e62cff,#ff8fdc,#ffe0f8)",
		"d3.interpolateRgbBasis(GEO_GLOW_STOPS)",
		"defs.append('radialGradient').attr('id','geo-glow-grad')",
		"d3.geoGraticule10()",
		"geoHotspotCount(value,max)",
		"geoGlowRadius(value,max)",
		"mix-blend-mode:screen",
		"const countryBorderLayer=svg.append('g').attr('class','geo-country-border-layer');",
		"const stateBorderLayer=svg.append('g').attr('class','geo-state-border-layer');",
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

func TestGeoMapUsesValueProportionalDotReveal(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"return t>0?Math.max(1,Math.min(12,Math.ceil(t*12))):0;",
		"var heatDots=geoIntensity(value,max)*12;",
		"var reveal=Math.max(0,Math.min(1,heatDots-index));",
		"dot.group.classed('is-on',false)",
		"dot.group.classed('is-on',true)",
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
