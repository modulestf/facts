package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const changesHeredoc = "../../testdata/changes/heredoc"

// TestChangesCLI checks the usage errors exit 2 with nothing on stdout, and
// that the JSON and text forms are written.
func TestChangesCLI(t *testing.T) {
	for name, args := range map[string][]string{
		"no base":       {"changes", changesHeredoc + "/head"},
		"base and diff": {"changes", changesHeredoc + "/head", "--base", changesHeredoc + "/base", "--diff", "x.json"},
		"zero timeout":  {"changes", changesHeredoc + "/head", "--base", changesHeredoc + "/base", "--timeout", "0s"},
		"no head":       {"changes", changesHeredoc + "/missing", "--base", changesHeredoc + "/base"},
	} {
		if code, out, _ := runCLI(args); code != exitError || out != "" {
			t.Errorf("%s: exit %d, stdout %q", name, code, out)
		}
	}
	code, out, errOut := runCLI([]string{"changes", changesHeredoc + "/head", "--base", changesHeredoc + "/base", "--json"})
	var r struct {
		SchemaVersion int `json:"schema_version"`
		Files         []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if code != exitClean || json.Unmarshal([]byte(out), &r) != nil || r.SchemaVersion != 1 || len(r.Files) != 1 || r.Files[0].Path != "bucket.tf" {
		t.Errorf("json: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runCLI([]string{"changes", changesHeredoc + "/head", "--base", changesHeredoc + "/base"})
	want := "bucket.tf modified, parsed, outside false\n" +
		"  aws_s3_bucket_policy.this edited, structural false\n    policy plain false/false, unread\n" +
		"  aws_s3_bucket_policy.odd edited, structural false\n    acl plain true/true, unread\n"
	if code != exitClean || !strings.HasPrefix(out, want) {
		t.Errorf("text: exit %d, got\n%s", code, out)
	}
}
