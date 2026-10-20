package main

import (
	"bytes"
	"testing"
)

func TestRules(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"rules"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "examples.versions-missing\ttool\nstructure.missing-description\ttool\nstructure.variable-untyped\tcandidate\n"
	if out.String() != want {
		t.Errorf("got %q", out.String())
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 || errb.Len() == 0 {
		t.Errorf("exit %d", code)
	}
}
