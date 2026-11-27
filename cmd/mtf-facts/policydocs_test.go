package main

import (
	"os"
	"strings"
	"testing"
)

// TestPolicyDocsCLI: the schema examples are the real output of two oracle
// cases, and every error exits 2 with nothing on stdout.
func TestPolicyDocsCLI(t *testing.T) {
	const cases, ex = "../../testdata/iam-policy-docs", "../../schema/examples"
	for args, want := range map[string]string{
		"iam-policy-docs " + cases + "/merge-local/head --base " + cases + "/merge-local/base": ex + "/iam-policy-docs.valid.json",
		"iam-policy-docs " + cases + "/refused/head":                                           ex + "/iam-policy-docs.valid.no-base.json",
	} {
		exp, err := os.ReadFile(want)
		if err != nil {
			t.Fatal(err)
		}
		if code, out, errOut := runCLI(strings.Fields(args)); code != exitClean || out != string(exp) {
			t.Errorf("%s: exit %d, stderr %q, stdout differs from %s:\n%s", args, code, errOut, want, out)
		}
	}
	for _, args := range [][]string{
		{"iam-policy-docs"},
		{"iam-policy-docs", cases + "/nosuch"},
		{"iam-policy-docs", cases + "/refused/head", "--base", cases + "/nosuch"},
		{"iam-policy-docs", cases + "/refused/head", "--timeout", "0s"},
		{"iam-policy-docs", cases + "/refused/head", "--diff", "files.json"},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
