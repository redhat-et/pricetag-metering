package dashboard

import (
	"strings"
	"testing"
)

func TestGeoMapUsesRaisedDeepSpaceStyle(t *testing.T) {
	data, err := FS.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"var GEO_LAND_COLOR='#29495b';",
		"var GEO_GLOW_STOPS=['#35566b','#6f91a0','#d47b3e','#ffb36c','#fff2d8'];",
		"background:linear-gradient(90deg,#28475b,#4d7890,#d47b3e,#ffb36c,#fff2d8)",
		"d3.interpolateRgbBasis(GEO_GLOW_STOPS)",
		"defs.append('radialGradient').attr('id','geo-glow-grad')",
		"geoHotspotCount(value,max)",
		"geoGlowRadius(value,max)",
		"mix-blend-mode:screen",
		"const countryBorderLayer=svg.append('g').attr('class','geo-country-border-layer');",
		"const stateBorderLayer=svg.append('g').attr('class','geo-state-border-layer');",
		"d3.geoNaturalEarth1().fitExtent",
		"geo-atmosphere",
		"geo-depth-shadow",
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
	for _, removed := range []string{"d3.geoGraticule10()", "filter:url(#geo-border-glow)", "const borderGlow="} {
		if strings.Contains(string(data), removed) {
			t.Errorf("dashboard.html still contains removed map treatment %q", removed)
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
		"const dotCentroid=code==='US'?projection([-74,40.5]):centroid;",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dashboard.html no longer contains %q", want)
		}
	}
}
