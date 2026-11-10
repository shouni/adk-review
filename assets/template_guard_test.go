package assets

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// This file checks that the embedded templates keep meeting the CSP's assumptions.
//
// The CSP that secureheaders builds limits script-src to 'self'. A single inline <script>
// or on* attribute stops that screen's scripts from running, and whoever added the template
// cannot tell (the other screens keep working).
//
// This app has actually hit it. Until 2026-08-24 review_form.html carried a 69-line
// inline script, which had to move to /static/js/review_form.js before the CSP went in.
// Of the 5 apps checked that day, the only ones still violating it were adk-review and
// ap-voice, which lacked this guard.

// templateNames returns the paths of every embedded template.
func templateNames(t *testing.T) []string {
	t.Helper()

	var names []string
	err := fs.WalkDir(Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Ext(p) == ".html" {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot list the templates: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no templates found (suspect the embed directive)")
	}
	return names
}

// TestTemplatesHaveNoInlineScripts ensures behaviour stays outside the templates. Inline,
// a screen's structure and behaviour mix in one file, which invites embedding template
// values in JS string literals (onclick="del('{{.JobID}}')"). It is also a precondition
// for keeping the CSP's script-src at 'self'.
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	t.Parallel()

	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	// Pick up only on*= in attribute position (a leading space is required so English words in text are not caught).
	eventAttribute := regexp.MustCompile(`\son[a-z]+\s*=\s*["']`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			for _, tag := range scriptTag.FindAllString(string(body), -1) {
				if !strings.Contains(tag, "src=") {
					t.Errorf("an inline script remains: %s", tag)
				}
			}
			for _, attribute := range eventAttribute.FindAllString(string(body), -1) {
				t.Errorf("an event handler attribute remains: %s", strings.TrimSpace(attribute))
			}
		})
	}
}

// TestTemplatesReferenceNoExternalOrigins keeps Bootstrap self-hosted. If a reference to an
// external origin returns, the CSP drops it at run time, so CI has to catch it.
func TestTemplatesReferenceNoExternalOrigins(t *testing.T) {
	t.Parallel()

	external := regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			for _, ref := range external.FindAllStringSubmatch(string(body), -1) {
				t.Errorf("a reference to an external origin has returned: %s", ref[1])
			}
		})
	}
}

// TestTemplateLocalAssetsExist checks that the /static/... paths the templates point to exist.
// A vendor version bump has to change both the templates and the directory name; changing
// only one gives a 404 (unnoticed until opened in a browser).
func TestTemplateLocalAssetsExist(t *testing.T) {
	t.Parallel()

	ref := regexp.MustCompile(`(?:href|src)="/static/([^"?]+)`)

	for _, name := range templateNames(t) {
		body, err := Templates.ReadFile(name)
		if err != nil {
			t.Fatalf("cannot read: %v", err)
		}
		for _, match := range ref.FindAllStringSubmatch(string(body), -1) {
			asset := path.Join("static", match[1])
			if _, err := fs.Stat(StaticFiles, asset); err != nil {
				t.Errorf("%s points to %s, which does not exist: %v", name, asset, err)
			}
		}
	}
}
