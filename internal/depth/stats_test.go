package depth

import (
	"slices"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/stats"
)

const statsTree = "../../testdata/diff-base"

// TestDeadlineMarksTimeout runs depth past its deadline, in the tree read
// and in the walk after it: each answers unwalked and marks a timeout.
func TestDeadlineMarksTimeout(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rec := stats.New()
		lim := facts.DefaultLimits
		lim.Deadline, lim.Stats = time.Now().Add(-time.Second), rec
		rep, err := Run(Options{Head: statsTree + "/head", Base: statsTree + "/base", Files: statsTree + "/files.json", Limits: lim})
		if err != nil || !slices.Contains(rep.Reasons, ReasonUnwalked) {
			t.Fatalf("report %+v, err %v", rep, err)
		}
		if got := rec.Record("depth", "v", 0).Outcome; got != stats.OutcomeTimeout {
			t.Errorf("outcome %s, want timeout", got)
		}
	})
	t.Run("walk", func(t *testing.T) {
		head, err := facts.ReadTree(statsTree+"/head", facts.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		base, err := facts.ReadTree(statsTree+"/base", facts.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		var list []fileEntry
		if err := readJSON(statsTree+"/files.json", &list, false); err != nil {
			t.Fatal(err)
		}
		rec := stats.New()
		o := Options{Head: statsTree + "/head", Base: statsTree + "/base", Limits: facts.DefaultLimits}
		o.Limits.Deadline, o.Limits.Stats = time.Now().Add(-time.Second), rec
		rep, err := (&run{o: o, reasons: map[string]bool{}}).evaluate(head, base, list)
		if err != nil || !slices.Contains(rep.Reasons, ReasonUnwalked) {
			t.Fatalf("report %+v, err %v", rep, err)
		}
		if got := rec.Record("depth", "v", 0).Outcome; got != stats.OutcomeTimeout {
			t.Errorf("outcome %s, want timeout", got)
		}
	})
}

// TestReadLimitIsNoTimeout runs depth on a tree over the read limit: the
// report is unwalked too, but no timeout is marked.
func TestReadLimitIsNoTimeout(t *testing.T) {
	rec := stats.New()
	lim := facts.DefaultLimits
	lim.MaxFiles, lim.Stats = 1, rec
	rep, err := Run(Options{Head: statsTree + "/head", Base: statsTree + "/base", Files: statsTree + "/files.json", Limits: lim})
	if err != nil || !slices.Contains(rep.Reasons, ReasonUnwalked) {
		t.Fatalf("report %+v, err %v", rep, err)
	}
	if got := rec.Record("depth", "v", 0).Outcome; got != stats.OutcomeCompleted {
		t.Errorf("outcome %s, want completed", got)
	}
}
