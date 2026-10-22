package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mergeFixtures = "../../testdata/merge"

func TestMergeCheck(t *testing.T) {
	dir := filepath.Join(mergeFixtures, "answered-once-duplicate")
	tests := []struct {
		name     string
		reply    string
		wantCode int
		wantOut  string
	}{
		{name: "valid", reply: "model-output.repair.json", wantCode: exitReplyValid, wantOut: `{"result":"valid"}` + "\n"},
		{name: "repair", reply: "model-output.json", wantCode: exitReplyRepair,
			wantOut: `{"result":"repair","reasons":["candidate C-001 is answered more than once"]}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{"merge", "check", filepath.Join(dir, "bundle.json"), filepath.Join(dir, tt.reply)}, &out, &errb)
			if code != tt.wantCode || out.String() != tt.wantOut || errb.Len() != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}

func TestMergeRefusesUsage(t *testing.T) {
	dir := filepath.Join(mergeFixtures, "dedup-step1")
	bundle, reply := filepath.Join(dir, "bundle.json"), filepath.Join(dir, "model-output.json")
	for name, args := range map[string][]string{
		"no verb":              {"merge"},
		"unknown verb":         {"merge", "apply"},
		"check one file":       {"merge", "check", bundle},
		"check a directory":    {"merge", "check", bundle, dir},
		"check broken bundle":  {"merge", "check", reply, reply},
		"final no registry":    {"merge", "final", bundle, reply},
		"final one file":       {"merge", "final", "--registry", reply, bundle},
		"final four files":     {"merge", "final", "--registry", reply, bundle, reply, reply, reply},
		"final bad registry":   {"merge", "final", "--registry", reply, bundle, reply},
		"final missing bundle": {"merge", "final", "--registry", reply, filepath.Join(dir, "nope.json"), reply},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != exitError || out.Len() != 0 || errb.Len() == 0 {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}

// TestMergeFinal runs merge final on a fixture with a repair turn, with the
// flags after the files, and compares the output with the fixture's
// expected findings, ids included.
func TestMergeFinal(t *testing.T) {
	reg := os.Getenv(registryEnv)
	if reg == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	dir := filepath.Join(mergeFixtures, "repair-still-invalid")
	var out, errb bytes.Buffer
	code := run([]string{
		"merge", "final",
		filepath.Join(dir, "bundle.json"), filepath.Join(dir, "model-output.json"), filepath.Join(dir, "model-output.repair.json"),
		"--registry", reg, "--templates", filepath.Join(mergeFixtures, "registry.json"),
	}, &out, &errb)
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	want, err := os.ReadFile(filepath.Join(dir, "expected-findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got, exp any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &exp); err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	e, _ := json.Marshal(exp)
	if !bytes.Equal(g, e) {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
	if !strings.HasSuffix(out.String(), "]\n") {
		t.Errorf("output does not end with a newline")
	}
}
