package analyze

import (
	"regexp"
	"sort"
	"strings"
)

// urlLiteral matches absolute http(s) URLs inside string literals.
var urlLiteral = regexp.MustCompile("[\"'`](https?://[^\"'`\\s]+)")

// validHost accepts DNS names with at least one dot (rejects template
// placeholders such as "{{.Host}}" or "${host}").
var validHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// ignoredHosts are hosts that never represent a runtime dependency.
var ignoredHosts = []string{
	"localhost", "127.0.0.1", "0.0.0.0", "example.com", "example.org", "example.net",
	"www.w3.org", "w3.org", "schema.org", "json-schema.org", "xmlns.com",
	"www.apple.com/DTDs", "purl.org", "opensource.org", "www.gnu.org", "spdx.org",
}

// ExtractHosts returns the distinct hosts of URLs used as string literals.
func ExtractHosts(src []byte) []string {
	seen := map[string]bool{}
	for _, m := range urlLiteral.FindAllSubmatch(src, -1) {
		u := string(m[1])
		host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
		if i := strings.IndexAny(host, "/?#:$"); i >= 0 {
			host = host[:i]
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if !validHost.MatchString(host) || ignored(host) {
			continue
		}
		seen[host] = true
	}
	return sortedSet(seen)
}

func ignored(host string) bool {
	for _, h := range ignoredHosts {
		if host == h || strings.HasSuffix(host, "."+h) || strings.HasPrefix(h, host+"/") {
			return true
		}
	}
	return strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.")
}

var (
	sqlRead  = regexp.MustCompile(`(?i)\bSELECT\b[^;"'` + "`" + `]*?\bFROM\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)`)
	sqlJoin  = regexp.MustCompile(`(?i)\bJOIN\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)`)
	sqlWrite = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bINSERT\s+(?:OR\s+\w+\s+)?INTO\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`(?i)\bUPDATE\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)\s+SET\b`),
		regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["\x60]?([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+["\x60]?([A-Za-z_][A-Za-z0-9_]*)`),
	}
	// sqlKeywordNoise filters English prose that happens to match ("select from the menu").
	sqlKeywordNoise = map[string]bool{
		"the": true, "a": true, "an": true, "this": true, "that": true, "these": true, "those": true,
		"your": true, "my": true, "our": true, "their": true, "its": true, "it": true, "them": true,
		"his": true, "her": true, "here": true, "there": true, "where": true, "which": true,
	}
	// stringLiteral matches double-quoted, single-quoted and backtick strings
	// (backticks and triple quotes may span lines).
	stringLiteral = regexp.MustCompile("(?s)`[^`]*`|\"\"\".*?\"\"\"|\"(?:[^\"\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\n]|\\\\.)*'")
)

// ExtractSQLTables returns the tables read and written by SQL statements
// found in the string literals of src (comments and prose are ignored).
func ExtractSQLTables(src []byte) (reads, writes []string) {
	r, w := map[string]bool{}, map[string]bool{}
	literals := stringLiteral.FindAll(src, -1)
	collect := func(re *regexp.Regexp, into map[string]bool) {
		for _, lit := range literals {
			collectFrom(re, lit, into)
		}
	}
	collect(sqlRead, r)
	collect(sqlJoin, r)
	for _, re := range sqlWrite {
		collect(re, w)
	}
	return sortedSet(r), sortedSet(w)
}

func collectFrom(re *regexp.Regexp, lit []byte, into map[string]bool) {
	for _, m := range re.FindAllSubmatch(lit, -1) {
		if name := strings.ToLower(string(m[1])); !sqlKeywordNoise[name] {
			into[name] = true
		}
	}
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
