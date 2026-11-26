package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const preCommitFixtures = "../../testdata/pre-commit"

func readFixture(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWrapperPinLayers holds each layer of WrapperPin to the pin each
// wrapper fixture records: the line reader alone gives it, and the
// structural confirmation never turns it into another result.
func TestWrapperPinLayers(t *testing.T) {
	ds, _ := filepath.Glob(filepath.Join(preCommitFixtures, "wrapper", "*"))
	if len(ds) == 0 {
		t.Fatal("no wrapper fixtures")
	}
	for _, d := range ds {
		want := strings.TrimSuffix(readFixture(t, filepath.Join(d, "pin")), "\n")
		data, err := os.ReadFile(filepath.Join(d, "config.yaml"))
		if os.IsNotExist(err) {
			continue // the command gives PinNone; there is nothing to read
		}
		if got, _ := wrapperPinLines(string(data)); got != want {
			t.Errorf("%s: line reader %q, want %q", filepath.Base(d), got, want)
		}
		if got := WrapperPin(data); got != want {
			t.Errorf("%s: WrapperPin %q, want %q", filepath.Base(d), got, want)
		}
	}
}

// TestDocsHookLayers does the same for DocsHook over the docs fixtures.
func TestDocsHookLayers(t *testing.T) {
	ds, _ := filepath.Glob(filepath.Join(preCommitFixtures, "docs", "*"))
	if len(ds) == 0 {
		t.Fatal("no docs fixtures")
	}
	for _, d := range ds {
		want := strings.SplitN(readFixture(t, filepath.Join(d, "want")), "\n", 2)[0] == "hook "+DocsHookProfile
		data, err := os.ReadFile(filepath.Join(d, "config.yaml"))
		if os.IsNotExist(err) {
			continue
		}
		if got := docsHookLines(string(data)); got != want {
			t.Errorf("%s: line reader %v, want %v", filepath.Base(d), got, want)
		}
		if got := DocsHook(data) == DocsHookProfile; got != want {
			t.Errorf("%s: DocsHook profile %v, want %v", filepath.Base(d), got, want)
		}
	}
}

// TestPreCommitConfirmation checks the structural confirmation on input the
// line readers accept and readYAML reads otherwise or not at all: a match
// becomes unreadable or custom.
func TestPreCommitConfirmation(t *testing.T) {
	pin := "repos:\n  - repo: https://github.com/antonbabenko/pre-commit-terraform\n    rev: v1.106.0\n    hooks:\n      - id: terraform_wrapper_module_for_each\n"
	docs := "repos:\n  - repo: https://github.com/antonbabenko/pre-commit-terraform\n    rev: v1.108.1\n    hooks:\n      - id: terraform_docs\n        args:\n          - --args=--lockfile=false\n"
	if got := WrapperPin([]byte(pin)); got != pinDerive {
		t.Fatalf("base pin: %q", got)
	}
	if got := DocsHook([]byte(docs)); got != DocsHookProfile {
		t.Fatalf("base docs: %q", got)
	}
	for name, s := range map[string]string{
		"top-level key twice": "default_stages: [x]\n" + pin + "default_stages: [y]\n",
		"hooks with a value":  strings.Replace(pin, "    hooks:\n", "    hooks: x\n", 1),
	} {
		if r, _ := wrapperPinLines(s); r != pinDerive {
			t.Errorf("%s: the line reader no longer reads derive; the case tests nothing", name)
		}
		if got := WrapperPin([]byte(s)); got != PinUnreadable {
			t.Errorf("%s: %q, want unreadable", name, got)
		}
	}
	for name, s := range map[string]string{
		"top-level key twice": "default_stages: x\n" + docs + "default_stages: y\n",
		"args not a list":     strings.Replace(docs, "        args:\n          - ", "        args:\n          ", 1),
	} {
		if !docsHookLines(s) && name == "top-level key twice" {
			t.Errorf("%s: the line reader no longer reads profile; the case tests nothing", name)
		}
		if got := DocsHook([]byte(s)); got != DocsHookCustom {
			t.Errorf("%s: %q, want custom", name, got)
		}
	}
	if pinConfirmed([]byte(pin), "v1.105.0") {
		t.Error("a rev other than the line reader's is confirmed")
	}
}

// TestWrapperResultModes checks the modes WrapperResult does not decide by
// itself: a pin's unknown result passes through, and at a copy pin derive is
// never run.
func TestWrapperResultModes(t *testing.T) {
	m := []byte("x = <<EOT\nEOT\n")
	for pin, want := range map[string]string{
		pinOldHook: pinOldHook, PinNone: PinNone, pinCopy: WrapperCopied, pinDerive: wrapperDeriveUndefined,
	} {
		if got := WrapperResult(pin, m, m); got != want {
			t.Errorf("%s: %q, want %q", pin, got, want)
		}
	}
	nul := []byte("terraform {\n  provider_meta \"aws\" {\n  }\n}\n\x00\n")
	if got := WrapperResult(pinDerive, nul, nul); got != wrapperProviderMeta {
		t.Errorf("a NUL byte beside a provider_meta line: %q", got)
	}
}

// FuzzWrapperDerive checks derive never panics, keeps its output within its
// own alphabet and size, and is a fixed point on what it derives.
func FuzzWrapperDerive(f *testing.F) {
	seedDir(f, "derive", "in.tf")
	f.Fuzz(func(t *testing.T, s []byte) {
		d, ok := WrapperDerive(s)
		if !ok {
			return
		}
		if len(d) > len(s) || !strings.HasSuffix(string(d), "\n") {
			t.Fatalf("derive grew or lost its final newline: %q", d)
		}
		if d2, ok := WrapperDerive(d); !ok || string(d2) != string(d) {
			t.Fatalf("derive is no fixed point on %q", d)
		}
	})
}

// FuzzPreCommitReaders checks the YAML and workflow readers never panic and
// give only their closed results, and that a match implies the line reader's.
func FuzzPreCommitReaders(f *testing.F) {
	seedDir(f, "wrapper", "config.yaml")
	seedDir(f, "docs", "config.yaml")
	seedDir(f, "docs", "workflow.yml")
	f.Fuzz(func(t *testing.T, s []byte) {
		switch p := WrapperPin(s); {
		case p == pinDerive || p == pinCopy:
			if r, _ := wrapperPinLines(string(s)); r != p {
				t.Fatalf("WrapperPin %q without the line reader", p)
			}
		case !strings.HasPrefix(p, "unknown "):
			t.Fatalf("WrapperPin %q", p)
		}
		if DocsHook(s) == DocsHookProfile && !docsHookLines(string(s)) {
			t.Fatal("DocsHook profile without the line reader")
		}
		switch w := DocsWorkflowPin(s, "v0.24.0"); w {
		case WorkflowSame, WorkflowMismatch, WorkflowNone, WorkflowUnreadable:
		default:
			t.Fatalf("DocsWorkflowPin %q", w)
		}
	})
}

// seedDir adds each fixture file of a set to the corpus.
func seedDir(f *testing.F, set, name string) {
	ps, _ := filepath.Glob(filepath.Join(preCommitFixtures, set, "*", name))
	for _, p := range ps {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
}
