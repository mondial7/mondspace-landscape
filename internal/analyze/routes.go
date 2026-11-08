package analyze

import "strings"

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
}

// SplitRoute splits a route pattern such as "GET /api/x/" into its method
// (possibly empty) and a normalised path without trailing slash.
func SplitRoute(route string) (method, path string) {
	route = strings.TrimSpace(route)
	if i := strings.IndexByte(route, ' '); i > 0 && httpMethods[strings.ToUpper(route[:i])] {
		method, route = strings.ToUpper(route[:i]), strings.TrimSpace(route[i+1:])
	}
	if i := strings.IndexAny(route, "?#"); i >= 0 {
		route = route[:i]
	}
	if len(route) > 1 {
		route = strings.TrimSuffix(route, "/")
	}
	return method, route
}

// MatchRoute finds the served route that best handles a request. Exact
// segment matches beat parameter matches, which beat subtree (prefix)
// matches; "/" is the catch-all of last resort.
func MatchRoute(routes []string, method, path string) (string, bool) {
	_, reqPath := SplitRoute(path)
	req := segments(reqPath)
	best, bestScore := "", -1
	for _, r := range routes {
		rm, rp := SplitRoute(r)
		if rm != "" && method != "" && rm != strings.ToUpper(method) {
			continue
		}
		score := matchScore(segments(rp), req)
		if score > bestScore {
			best, bestScore = r, score
		}
	}
	return best, bestScore >= 0
}

// matchScore returns -1 for no match; higher is more specific.
func matchScore(route, req []string) int {
	if len(route) > len(req) {
		return -1
	}
	score := 0
	for i, seg := range route {
		switch {
		case seg == req[i]:
			score += 3
		case isParam(seg) || isParam(req[i]):
			score += 2
		default:
			return -1
		}
	}
	if len(route) == len(req) {
		return score + 1 // full match beats prefix match
	}
	return score
}

func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") || strings.HasPrefix(seg, ":") || strings.Contains(seg, "${") || seg == "*"
}

func segments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
