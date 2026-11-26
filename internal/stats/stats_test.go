package stats

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNilRecorderIsOff(t *testing.T) {
	var r *Recorder
	r.Phase("read_head")
	r.Parse()(3, 10)
	r.Rule("x")()
	r.Read(1, 2)
	r.Timeout()
	if got := r.Record("lint", "v", 0); got != nil {
		t.Fatalf("Record of a nil recorder = %+v, want nil", got)
	}
}

func TestPhasesAreSequential(t *testing.T) {
	r := New()
	r.Phase("read_head")
	time.Sleep(2 * time.Millisecond)
	r.Phase("areas")
	r.Phase("render")
	rec := r.Record("lint", "v1", 3)
	var names []string
	var sum int64
	for _, p := range rec.Phases {
		names = append(names, p.Name)
		sum += p.WallNS
	}
	if got := strings.Join(names, ","); got != "run,read_head,areas,render" {
		t.Fatalf("phases %s, want run,read_head,areas,render", got)
	}
	if rec.Phases[1].WallNS < int64(2*time.Millisecond) {
		t.Errorf("read_head took %d ns, want at least 2 ms", rec.Phases[1].WallNS)
	}
	if sum > rec.WallNS {
		t.Errorf("phases sum %d ns over the wall time %d ns", sum, rec.WallNS)
	}
	var cpu int64
	for _, p := range rec.Phases {
		if p.CPUNS == nil {
			t.Fatalf("phase %s has no cpu_ns", p.Name)
		}
		cpu += *p.CPUNS
	}
	if total := *rec.CPUUserNS + *rec.CPUSystemNS; cpu > total {
		t.Errorf("phases used %d ns of CPU, over the process total %d ns", cpu, total)
	}
	if rec.Outcome != OutcomeCompleted || rec.ExitCode != 3 || rec.Command != "lint" || rec.SchemaVersion != SchemaVersion {
		t.Errorf("record head %+v", rec)
	}
	if rec.StartedUnixNS <= 0 {
		t.Errorf("started_unix_ns %d", rec.StartedUnixNS)
	}
}

func TestOutcome(t *testing.T) {
	for _, tc := range []struct {
		code    int
		timeout bool
		want    string
	}{
		{0, false, OutcomeCompleted},
		{1, false, OutcomeCompleted},
		{3, false, OutcomeCompleted},
		{2, false, OutcomeError},
		{2, true, OutcomeTimeout},
		{0, true, OutcomeTimeout}, // depth answers a timeout with exit 0
	} {
		r := New()
		if tc.timeout {
			r.Timeout()
		}
		if got := r.Record("c", "v", tc.code).Outcome; got != tc.want {
			t.Errorf("code %d timeout %v: outcome %s, want %s", tc.code, tc.timeout, got, tc.want)
		}
	}
}

func TestAggregates(t *testing.T) {
	r := New()
	r.Read(3, 300)
	r.Read(2, 20)
	r.Parse()(2, 50)
	r.Parse()(1, 5)
	r.Rule("b.rule")()
	r.Rule("a.rule")()
	r.Rule("b.rule")()
	rec := r.Record("lint", "v", 0)
	if rec.FilesRead != 5 || rec.BytesRead != 320 {
		t.Errorf("read %d files %d bytes, want 5 and 320", rec.FilesRead, rec.BytesRead)
	}
	if rec.Parse.Calls != 2 || rec.Parse.Files != 3 || rec.Parse.Bytes != 55 {
		t.Errorf("parse %+v", rec.Parse)
	}
	if len(rec.Rules) != 2 || rec.Rules[0].ID != "a.rule" || rec.Rules[1].ID != "b.rule" || rec.Rules[1].Calls != 2 {
		t.Errorf("rules %+v, want a.rule then b.rule with 2 calls", rec.Rules)
	}
}

func TestEmptyListsAreArrays(t *testing.T) {
	b, err := json.Marshal(New().Record("depth", "v", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"rules":[]`) {
		t.Errorf("rules not an empty array: %s", b)
	}
}

// TestSnapshotRace records from a worker while the main goroutine takes
// the record, as a timeout does. go test -race catches a missing lock.
func TestSnapshotRace(_ *testing.T) {
	r := New()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			r.Phase("p")
			r.Rule("x")()
			r.Parse()(1, 1)
			r.Read(1, 1)
		}
	})
	for range 50 {
		_ = r.Record("lint", "v", 2)
	}
	wg.Wait()
}

// TestRecordCopies checks that a record taken while the worker goes on
// does not change after it is taken.
func TestRecordCopies(t *testing.T) {
	r := New()
	r.Rule("x")()
	rec := r.Record("lint", "v", 2)
	r.Rule("x")()
	r.Phase("later")
	if rec.Rules[0].Calls != 1 || len(rec.Phases) != 1 {
		t.Errorf("record changed after it was taken: %+v", rec)
	}
}
