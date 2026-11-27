package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const noPatchMain = "resource \"aws_s3_bucket\" \"this\" {\n  bucket = var.name\n}\n\nvariable \"name\" {\n  type = string\n}\n"

// noPatchCase is a files list entry whose base content the list does not
// show: at is the base path left unknown, and base the merge base file a
// host would hold there, which depth reads from its own tree.
type noPatchCase struct {
	entry, at, base string
}

var noPatchCases = map[string]noPatchCase{
	"removed, 0 and 0":          {`{"filename":"variables.tf","status":"removed","additions":0,"deletions":0}`, "variables.tf", "# moved out\n"},
	"removed, no counts":        {`{"filename":"variables.tf","status":"removed"}`, "variables.tf", "# moved out\n"},
	"removed, deletions":        {`{"filename":"variables.tf","status":"removed","additions":0,"deletions":3}`, "variables.tf", "# moved out\n"},
	"removed, blank patch":      {`{"filename":"variables.tf","status":"removed","patch":"","additions":0,"deletions":0}`, "variables.tf", "# moved out\n"},
	"removed, whitespace patch": {`{"filename":"variables.tf","status":"removed","patch":"  \n","additions":0,"deletions":0}`, "variables.tf", "# moved out\n"},
	"modified, 0 and 0":         {`{"filename":"main.tf","status":"modified","additions":0,"deletions":0}`, "main.tf", noPatchMain},
	"modified, no counts":       {`{"filename":"main.tf","status":"modified"}`, "main.tf", noPatchMain},
	"modified, null counts":     {`{"filename":"main.tf","status":"modified","additions":null,"deletions":null}`, "main.tf", noPatchMain},
	"changed, 0 and 0":          {`{"filename":"main.tf","status":"changed","additions":0,"deletions":0}`, "main.tf", noPatchMain},
	"changed, negative counts":  {`{"filename":"main.tf","status":"changed","additions":-1,"deletions":-1}`, "main.tf", noPatchMain},
	"renamed, 0 and 0":          {`{"filename":"main.tf","status":"renamed","previous_filename":"old.tf","additions":0,"deletions":0}`, "old.tf", noPatchMain},
	"renamed, no counts":        {`{"filename":"main.tf","status":"renamed","previous_filename":"old.tf"}`, "old.tf", noPatchMain},
}

// noPatchTrees writes the head, the merge base and the files list of c.
func noPatchTrees(t *testing.T, c noPatchCase) (head, base, files string) {
	t.Helper()
	dir := t.TempDir()
	head, base = filepath.Join(dir, "head"), filepath.Join(dir, "base")
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{head, base} {
		write(filepath.Join(root, "examples/complete/main.tf"), "module \"x\" {\n  source = \"../../\"\n  name   = \"n\"\n}\n")
	}
	write(filepath.Join(head, "main.tf"), noPatchMain)
	if c.at == "variables.tf" {
		write(filepath.Join(base, "main.tf"), noPatchMain) // a rename leaves no main.tf at the base
	}
	write(filepath.Join(base, c.at), c.base)
	files = filepath.Join(dir, "files.json")
	write(files, "["+c.entry+"]")
	return head, base, files
}

// TestNoPatchNeverInert runs each command on a files list entry the base
// content of which the list does not show, a removed .tf file with no patch
// whatever its counts above all: as a host lists a file a rename moved out of
// the tree. No command may read it as an empty or unchanged file.
func TestNoPatchNeverInert(t *testing.T) {
	t.Parallel()
	for name, c := range noPatchCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			head, base, files := noPatchTrees(t, c)
			dir := filepath.Dir(files)

			code, beh, errOut := runCLI([]string{"behaviour", head, "--json", "--diff", files})
			var b struct {
				Inert    bool
				Triggers []struct{ File, Category, Reason string }
			}
			if err := json.Unmarshal([]byte(beh), &b); code != exitClean || err != nil {
				t.Fatalf("behaviour: exit %d, %v, stderr %q", code, err, errOut)
			}
			if b.Inert || !slices.ContainsFunc(b.Triggers, func(tr struct{ File, Category, Reason string }) bool {
				return tr.File == c.at && tr.Category == "unclassified-change" && strings.Contains(tr.Reason, "no patch")
			}) {
				t.Errorf("behaviour: %s", beh)
			}

			code, aff, errOut := runCLI([]string{"affected", head, "--json", "--diff", files})
			var a struct {
				Inert    bool
				Examples []struct{ Dir string }
			}
			if err := json.Unmarshal([]byte(aff), &a); code != exitClean || err != nil {
				t.Fatalf("affected: exit %d, %v, stderr %q", code, err, errOut)
			}
			if a.Inert || len(a.Examples) != 1 || a.Examples[0].Dir != "examples/complete" {
				t.Errorf("affected: %s", aff)
			}

			code, chg, errOut := runCLI([]string{"changes", head, "--json", "--diff", files})
			var ch struct {
				Files []struct {
					Path   string
					Parsed bool
					Reason *string
				}
			}
			if err := json.Unmarshal([]byte(chg), &ch); code != exitClean || err != nil {
				t.Fatalf("changes: exit %d, %v, stderr %q", code, err, errOut)
			}
			if !slices.ContainsFunc(ch.Files, func(f struct {
				Path   string
				Parsed bool
				Reason *string
			}) bool {
				return f.Path == c.at && !f.Parsed && f.Reason != nil && *f.Reason == "no_patch"
			}) {
				t.Errorf("changes: %s", chg)
			}

			code, lint, errOut := runCLI([]string{"lint", head, "--json", "--diff", files})
			if code != exitNotEvaluated || !strings.Contains(lint, `"no_base"`) {
				t.Errorf("lint: exit %d, stderr %q, no no_base rule", code, errOut)
			}

			bf, af := filepath.Join(dir, "behaviour.json"), filepath.Join(dir, "affected.json")
			for p, s := range map[string]string{bf: beh, af: aff} {
				if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			code, dep, errOut := runCLI([]string{"depth", "--head", head, "--base", base, "--files", files, "--classify", bf, "--affected", af})
			var d struct {
				Depth   string
				Reasons []string
			}
			if err := json.Unmarshal([]byte(dep), &d); code != exitClean || err != nil {
				t.Fatalf("depth: exit %d, %v, stderr %q", code, err, errOut)
			}
			if d.Depth != "deep" || !slices.Contains(d.Reasons, "deep-classifier-category") {
				t.Errorf("depth: %s", dep)
			}
		})
	}
}

// TestNoPatchUnifiedRenameProven: a unified diff states a rename with no
// hunk as such, so the base keeps the head content and the rename stays
// inert; a files list says the same with 0 and 0 and is not trusted.
func TestNoPatchUnifiedRenameProven(t *testing.T) {
	t.Parallel()
	head, _, files := noPatchTrees(t, noPatchCases["renamed, 0 and 0"])
	diff := filepath.Join(filepath.Dir(files), "change.diff")
	if err := os.WriteFile(diff, []byte("diff --git a/old.tf b/main.tf\nsimilarity index 100%\nrename from old.tf\nrename to main.tf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCLI([]string{"behaviour", head, "--json", "--diff", diff}); code != exitClean || !strings.Contains(out, `"inert": true`) {
		t.Errorf("unified rename: exit %d, stderr %q, stdout:\n%s", code, errOut, out)
	}
}

// TestNoPatchBadEntryRefused: a count that is no number and a status that is
// no GitHub status stop the run.
func TestNoPatchBadEntryRefused(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		`{"filename":"main.tf","status":"modified","additions":"0","deletions":0}`,
		`{"filename":"main.tf","status":"pure-rename","previous_filename":"old.tf","additions":500,"deletions":500}`,
		`{"filename":"main.tf","status":"mode-only"}`,
	} {
		head, _, files := noPatchTrees(t, noPatchCase{entry, "main.tf", noPatchMain})
		for _, cmd := range []string{"behaviour", "affected", "changes", "lint"} {
			if code, out, errOut := runCLI([]string{cmd, head, "--json", "--diff", files}); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
				t.Errorf("%s %s: exit %d, stdout %q, stderr %q", cmd, entry, code, out, errOut)
			}
		}
	}
}
