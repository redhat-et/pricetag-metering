package dashboard

import (
	"bytes"
	"testing"
)

// The usage pages cover one data source each: the EnMaaS gateway. Keep the
// disclaimer visible so users do not read the numbers as organisation-wide AI
// spend when other tools or platforms are also in use.
func TestUsagePagesStateEnMaaSScope(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{
			file: "account.html",
			want: "This page shows usage that went through the EnMaaS gateway.",
		},
		{
			file: "manager.html",
			want: "This page shows EnMaaS usage for your team.",
		},
	} {
		data, err := FS.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(tc.want)) {
			t.Errorf("%s: scope disclaimer missing %q", tc.file, tc.want)
		}
		if !bytes.Contains(data, []byte("EnMaaS traffic only.")) {
			t.Errorf("%s: scope disclaimer heading missing", tc.file)
		}
	}
}
