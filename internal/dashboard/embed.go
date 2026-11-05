package dashboard

import "embed"

//go:embed dashboard.html manager.html admin.html routing.html login.html user-dashboard.html welcome.html enmaas-welcome.html coming-soon.html account.html
var FS embed.FS
