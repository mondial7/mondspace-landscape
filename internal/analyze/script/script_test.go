package script

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

func run(t *testing.T, a analyze.Analyzer, fsys fstest.MapFS) analyze.Facts {
	t.Helper()
	f, _ := timedRun(t, a, fsys)
	return f
}

// timedRun is run that also reports how long Analyze (not the scan) took.
func timedRun(t *testing.T, a analyze.Analyzer, fsys fstest.MapFS) (analyze.Facts, time.Duration) {
	t.Helper()
	repo, err := scan.Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	f, err := a.Analyze(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return f, time.Since(start)
}

func unitByID(t *testing.T, f analyze.Facts, id string) analyze.Unit {
	t.Helper()
	for _, u := range f.Units {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("unit %q not in %+v", id, f.Units)
	return analyze.Unit{}
}

func depStrings(f analyze.Facts) []string {
	var out []string
	for _, d := range f.Deps {
		out = append(out, d.From+"->"+d.To)
	}
	sort.Strings(out)
	return out
}

func useStrings(f analyze.Facts) []string {
	var out []string
	for _, u := range f.Uses {
		who := u.Unit
		if who == "" {
			who = u.File
		}
		out = append(out, who+"->"+u.External)
	}
	sort.Strings(out)
	return out
}

func callStrings(f analyze.Facts) []string {
	var out []string
	for _, c := range f.Calls {
		out = append(out, c.File+" "+c.Method+" "+c.Path)
	}
	sort.Strings(out)
	return out
}

func hasTag(u analyze.Unit, tag string) bool {
	for _, t := range u.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func TestJavaScript(t *testing.T) {
	fsys := fstest.MapFS{
		"package.json":       {Data: []byte(`{"name":"shop","main":"server/index.js"}`)},
		"server/index.js":    {Data: []byte("const express = require('express')\nconst db = require('./db')\nconst app = express()\napp.get('/api/items', h)\napp.post(\"/api/items/:id\", h)\napp.listen(3000)\n")},
		"server/db.js":       {Data: []byte("import pg from 'pg'\nexport const pool = new pg.Pool()\n")},
		"client/src/App.tsx": {Data: []byte("import React from 'react'\nimport { api } from '../lib/api'\nexport default function App() { return <div/> }\n")},
		"client/lib/api.ts": {Data: []byte("import Anthropic from '@anthropic-ai/sdk'\n" +
			"export const list = () => fetch('/api/items')\n" +
			"export const buy = (id) => fetch(`/api/items/${id}`, { method: 'POST' })\n")},
		"client/src/App.test.tsx": {Data: []byte("import { App } from './App'\nfetch('/api/never')\n")},
	}
	f := run(t, NewJS(), fsys)

	srv := unitByID(t, f, "js:server")
	if !srv.Entry || !hasTag(srv, "http-server") {
		t.Errorf("server unit = %+v, want entry http-server", srv)
	}
	if want := []string{"GET /api/items", "POST /api/items/:id"}; !reflect.DeepEqual(srv.Routes, want) {
		t.Errorf("routes = %v, want %v", srv.Routes, want)
	}
	if app := unitByID(t, f, "js:client/src"); !hasTag(app, "ui") {
		t.Errorf("client/src tags = %v, want ui", app.Tags)
	}
	if want := []string{"js:client/src->js:client/lib"}; !reflect.DeepEqual(depStrings(f), want) {
		t.Errorf("deps = %v, want %v", depStrings(f), want)
	}
	if want := []string{"js:client/lib->anthropic", "js:server->postgres"}; !reflect.DeepEqual(useStrings(f), want) {
		t.Errorf("uses = %v, want %v", useStrings(f), want)
	}
	want := []string{"client/lib/api.ts  /api/items", "client/lib/api.ts POST /api/items/${id}"}
	if got := callStrings(f); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestPython(t *testing.T) {
	fsys := fstest.MapFS{
		"app/main.py":     {Data: []byte("from fastapi import FastAPI\nfrom app.db import session\nfrom .models import Item\napp = FastAPI()\n\n@app.get('/items')\ndef items(): pass\n\n@app.post(\"/items/{id}\")\ndef buy(): pass\n\nif __name__ == '__main__':\n    run()\n")},
		"app/db.py":       {Data: []byte("import sqlalchemy\nimport boto3\n")},
		"app/models.py":   {Data: []byte("class Item: pass\n")},
		"scripts/tool.py": {Data: []byte("import subprocess\nimport requests\n")},
		"tests/test_x.py": {Data: []byte("import app.main\n")},
		"app/__init__.py": {Data: []byte("")},
	}
	f := run(t, NewPython(), fsys)
	app := unitByID(t, f, "py:app")
	if !app.Entry || !hasTag(app, "http-server") {
		t.Errorf("app = %+v, want entry http-server", app)
	}
	if want := []string{"GET /items", "POST /items/{id}"}; !reflect.DeepEqual(app.Routes, want) {
		t.Errorf("routes = %v, want %v", app.Routes, want)
	}
	if want := []string{"py:app->aws", "py:app->sql", "py:scripts->os-processes"}; !reflect.DeepEqual(useStrings(f), want) {
		t.Errorf("uses = %v, want %v", useStrings(f), want)
	}
	if want := []string{"py:tests->py:app"}; !reflect.DeepEqual(depStrings(f), want) || f.Deps[0].Label != analyze.TestsLabel {
		t.Errorf("deps = %v (%+v), want only the test dependency", depStrings(f), f.Deps)
	}
}

func TestSwift(t *testing.T) {
	fsys := fstest.MapFS{
		"GiftsApp/GiftsApp.swift":                       {Data: []byte("import SwiftUI\nimport GiftsKit\n\n@main\nstruct GiftsApp: App { let store = GiftStore() }\n")},
		"GiftsApp/Views/List.swift":                     {Data: []byte("import SwiftUI\nimport StoreKit\nstruct ListView: View {}\n")},
		"GiftsKit/Package.swift":                        {Data: []byte("// swift-tools-version:5.9\nimport PackageDescription\n")},
		"GiftsKit/Sources/GiftsKit/Store.swift":         {Data: []byte("import Foundation\nimport CloudKit\npublic final class GiftStore {}\n")},
		"GiftsKit/Tests/GiftsKitTests/StoreTests.swift": {Data: []byte("import XCTest\n@testable import GiftsKit\nfinal class StoreTests: XCTestCase { func testX() { _ = GiftStore() } }\n")},
	}
	f := run(t, NewSwift(), fsys)
	app := unitByID(t, f, "swift:GiftsApp")
	if !app.Entry || !hasTag(app, "ui") {
		t.Errorf("app = %+v, want ui entry", app)
	}
	want := []string{"swift:GiftsApp->swift:GiftsKit/Sources/GiftsKit", "swift:GiftsKit/Tests/GiftsKitTests->swift:GiftsKit/Sources/GiftsKit"}
	if !reflect.DeepEqual(depStrings(f), want) {
		t.Errorf("deps = %v, want %v", depStrings(f), want)
	}
	for _, d := range f.Deps {
		if d.From == "swift:GiftsKit/Tests/GiftsKitTests" && d.Label != "tests" {
			t.Errorf("test dep label = %q, want tests", d.Label)
		}
	}
	if want := []string{"swift:GiftsApp/Views->appstore", "swift:GiftsKit/Sources/GiftsKit->icloud"}; !reflect.DeepEqual(useStrings(f), want) {
		t.Errorf("uses = %v, want %v", useStrings(f), want)
	}
	if store := unitByID(t, f, "swift:GiftsKit/Sources/GiftsKit"); store.Root != "GiftsKit" {
		t.Errorf("store root = %q, want GiftsKit (SwiftPM module)", store.Root)
	}
	if views := unitByID(t, f, "swift:GiftsApp/Views"); views.Root != "GiftsApp" {
		t.Errorf("views root = %q, want GiftsApp (Xcode target)", views.Root)
	}
	for _, u := range f.Units {
		if u.ID == "swift:GiftsKit" {
			t.Error("Package.swift is a manifest, not a code unit")
		}
	}
}

func TestHTML(t *testing.T) {
	fsys := fstest.MapFS{
		"web/templates/layout.html": {Data: []byte(`<link href="/static/app.css"><a href="/clients/{{.ID}}">x</a><a href="https://example.com">e</a>`)},
		"web/templates/form.html":   {Data: []byte(`<form method="post" action="/sessions/start"></form><button hx-post="/sessions/{{.ID}}/pause">p</button>`)},
		"web/static/app.css":        {Data: []byte("body{}")},
	}
	f := run(t, NewHTML(), fsys)
	tpl := unitByID(t, f, "web:web/templates")
	if !hasTag(tpl, "ui") || !hasTag(tpl, "templates") {
		t.Errorf("templates tags = %v", tpl.Tags)
	}
	if st := unitByID(t, f, "web:web/static"); !hasTag(st, "assets") || st.Language != "css" {
		t.Errorf("static = %+v, want css assets", st)
	}
	want := []string{
		"web/templates/form.html POST /sessions/start",
		"web/templates/form.html POST /sessions/{{.ID}}/pause",
		"web/templates/layout.html GET /clients/{{.ID}}",
		"web/templates/layout.html GET /static/app.css",
	}
	if got := callStrings(f); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestSwiftSymbolReferencesRefineDependencies(t *testing.T) {
	fsys := fstest.MapFS{
		"App/App.swift":                     {Data: []byte("import SwiftUI\nimport Kit\n@main\nstruct GiftsApp: App { var body: some Scene { WindowGroup { ListView() } } }\n")},
		"App/Views/ListView.swift":          {Data: []byte("import SwiftUI\nimport Kit\nstruct ListView: View {\n  let store: GiftStore\n  var body: some View { Text(\"x\").foregroundStyle(Theme.accent) }\n}\n")},
		"App/UI/Theme.swift":                {Data: []byte("import SwiftUI\nenum Theme { static let accent = Color.blue }\n")},
		"Kit/Package.swift":                 {Data: []byte("// swift-tools-version:5.9\n")},
		"Kit/Sources/Kit/Store/Store.swift": {Data: []byte("public final class GiftStore {}\n")},
		"Kit/Sources/Kit/Domain/Gift.swift": {Data: []byte("public struct Gift {}\n")},
	}
	f := run(t, NewSwift(), fsys)
	want := []string{
		"swift:App->swift:App/Views",                   // intra-module: App uses ListView
		"swift:App/Views->swift:App/UI",                // intra-module: ListView uses Theme
		"swift:App/Views->swift:Kit/Sources/Kit/Store", // cross-module: only what is referenced
	}
	if got := depStrings(f); !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v\nwant   %v", got, want)
	}
}

func TestScriptDepsRecordTheImportingFile(t *testing.T) {
	fsys := fstest.MapFS{
		"web/a.js": {Data: []byte("import { x } from '../lib/x'\n")},
		"web/b.js": {Data: []byte("import { x } from '../lib/x'\n")},
		"lib/x.js": {Data: []byte("export const x = 1\n")},
	}
	f := run(t, NewJS(), fsys)
	var got []string
	for _, d := range f.Deps {
		got = append(got, d.File+":"+d.From+"->"+d.To)
	}
	want := []string{"web/a.js:js:web->js:lib", "web/b.js:js:web->js:lib"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestJSFetchMethodStaysInsideTheCall(t *testing.T) {
	fsys := fstest.MapFS{
		"web/api.js": {Data: []byte("fetch('/a', {method: 'POST'})\n" +
			"fetch('/b', {\n  headers: {},\n  method: \"delete\",\n})\n" +
			"fetch('/c')\nconst o = {method: 'PUT'}\n" +
			"fetch('/d'\n" + strings.Repeat(" ", 1024) + "method: 'PATCH'\n")},
	}
	want := []string{"web/api.js  /c", "web/api.js  /d", "web/api.js DELETE /b", "web/api.js POST /a"}
	if got := callStrings(run(t, NewJS(), fsys)); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// Unclosed fetch( calls must not make the scan quadratic in file size.
func TestJSFetchScanIsLinear(t *testing.T) {
	n := 95_000 // just under scan.MaxFileSize
	if raceEnabled {
		n = 5_000
	}
	fsys := fstest.MapFS{"web/big.js": {Data: []byte(strings.Repeat("fetch('/a'\n", n))}}
	f, d := timedRun(t, NewJS(), fsys)
	if d > time.Second && !raceEnabled {
		t.Errorf("1 MB of unclosed fetch calls took %v", d)
	}
	if len(f.Calls) != n {
		t.Errorf("calls = %d, want %d", len(f.Calls), n)
	}
}

func TestJSRelativeImportsResolve(t *testing.T) {
	fsys := fstest.MapFS{
		"a/main.js":       {Data: []byte("import x from './x'\nimport y from '../b/y'\nimport y2 from '../b/y'\nimport c from '../c'\nimport e from './x.js'\nimport n from '../b/none'\nimport z from '../b/y.test'\n")},
		"a/x.test.js":     {Data: []byte("")},
		"b/y.ts":          {Data: []byte("")},
		"b/y.test.helper": {Data: []byte("")},
		"c/index.js":      {Data: []byte("")},
	}
	want := []string{"js:a->js:b", "js:a->js:c"}
	if got := depStrings(run(t, NewJS(), fsys)); !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

// Relative imports must not cost a scan of every repository file each.
func TestJSRelativeImportsScale(t *testing.T) {
	fsys := fstest.MapFS{}
	var imports strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&imports, "import m%d from './missing%d'\n", i, i)
	}
	n := 4000
	if raceEnabled {
		n = 200
	}
	for i := 0; i < n; i++ {
		fsys[fmt.Sprintf("src/d%d/f%d.js", i%50, i)] = &fstest.MapFile{Data: []byte(imports.String())}
	}
	if _, d := timedRun(t, NewJS(), fsys); d > time.Second && !raceEnabled {
		t.Errorf("4000 files x 40 relative imports took %v", d)
	}
}
