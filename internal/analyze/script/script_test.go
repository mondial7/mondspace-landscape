package script

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

func run(t *testing.T, a analyze.Analyzer, fsys fstest.MapFS) analyze.Facts {
	t.Helper()
	repo, err := scan.Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	f, err := a.Analyze(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return f
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
