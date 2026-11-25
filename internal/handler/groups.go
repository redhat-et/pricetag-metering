package handler

import (
	"sort"
	"strings"
)

// technicalGroups are IdP plumbing that must never count as business
// authorization. The Stage realm currently ships these in the groups claim
// because the mapper is a User Realm Role mapper.
var technicalGroups = map[string]struct{}{
	"offline_access":    {},
	"uma_authorization": {},
	"account":           {},
}

// groupCommonName reduces one group identifier to its comparable name. The
// directory sync stores LDAP memberOf values as DNs
// (CN=enmaas-users,OU=Groups,DC=redhat,DC=com) and some IdPs use path-shaped
// groups (/enmaas-users); both must match the configured guest-list name.
func groupCommonName(g string) string {
	g = strings.TrimSpace(g)
	if g == "" {
		return ""
	}
	if strings.Contains(g, "=") {
		for _, part := range strings.Split(g, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), "cn") {
				if cn := strings.TrimSpace(kv[1]); cn != "" {
					return cn
				}
			}
		}
	}
	if i := strings.LastIndex(g, "/"); i >= 0 {
		g = g[i+1:]
	}
	return strings.TrimSpace(g)
}

// NormalizeGroups trims, reduces DNs/paths to their name, drops technical
// roles, de-duplicates, and sorts.
func NormalizeGroups(groups []string) []string {
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		g = groupCommonName(g)
		if g == "" {
			continue
		}
		lower := strings.ToLower(g)
		if _, technical := technicalGroups[lower]; technical {
			continue
		}
		if strings.HasPrefix(lower, "default-roles-") || strings.HasSuffix(lower, "_authorization") {
			continue
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
