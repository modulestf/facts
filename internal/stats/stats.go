// Package stats records where one mtf-facts run spends its time and memory:
// sequential phases, the time spent parsing, the time of each rule, the
// files and bytes read and the process totals. A command writes the record
// to a file only when asked; it never changes what the command prints.
//
// A nil *Recorder is off: every method does nothing, so the engine calls
// them unconditionally. A Recorder is safe for concurrent use, so the main
// goroutine can take a record while an abandoned worker still runs.
package stats

import (
	"maps"
	"runtime/metrics"
	"slices"
	"sync"
	"time"
)

// SchemaVersion is the schema_version of the record, schema/stats.schema.json.
const SchemaVersion = 1

// Outcomes of a run.
const (
	OutcomeCompleted = "completed" // the command wrote its report, findings or not
	OutcomeError     = "error"     // the command exited 2 on bad input, a limit or a failure
	OutcomeTimeout   = "timeout"   // the command's own timeout ended the run
)

// exitError is the exit code of a failed run, as the CLI defines it.
const exitError = 2

// allocMetric is the Go heap bytes allocated since the process started.
const allocMetric = "/gc/heap/allocs:bytes"

// Record is what a command writes. Times are integer nanoseconds and sizes
// integer bytes, so a host can sum the records of many calls exactly; it
// takes the maximum of MaxRSSBytes instead.
type Record struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          Tool   `json:"tool"`
	Command       string `json:"command"`
	Outcome       string `json:"outcome"`
	ExitCode      int    `json:"exit_code"`
	StartedUnixNS int64  `json:"started_unix_ns"`
	WallNS        int64  `json:"wall_ns"`
	// CPUUserNS, CPUSystemNS and MaxRSSBytes are the process totals the
	// operating system reports; nil where it reports none.
	CPUUserNS   *int64 `json:"cpu_user_ns"`
	CPUSystemNS *int64 `json:"cpu_system_ns"`
	MaxRSSBytes *int64 `json:"max_rss_bytes"`
	// HeapAllocBytes is the Go heap allocated during the run, freed or not.
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	// FilesRead and BytesRead count the regular files read from input trees.
	FilesRead int64   `json:"files_read"`
	BytesRead int64   `json:"bytes_read"`
	Phases    []Phase `json:"phases"`
	Parse     Parse   `json:"parse"`
	Rules     []Rule  `json:"rules"`
}

// Tool names the binary that wrote the record.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Phase is one step of the run. Phases do not overlap and follow each
// other in run order. CPUNS is the process CPU time, user and system,
// during the phase: unlike WallNS it does not grow while the process
// waits for a busy machine. It is nil where the system reports none.
type Phase struct {
	Name           string `json:"name"`
	WallNS         int64  `json:"wall_ns"`
	CPUNS          *int64 `json:"cpu_ns"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
}

// Parse sums every parse of a directory of .tf files. Its time is also
// counted in the phase each parse ran in.
type Parse struct {
	Calls  int64 `json:"calls"`
	Files  int64 `json:"files"`
	Bytes  int64 `json:"bytes"`
	WallNS int64 `json:"wall_ns"`
}

// Rule sums the evaluations of one rule over every area. Its time is also
// counted in the phase the rules ran in.
type Rule struct {
	ID     string `json:"id"`
	Calls  int64  `json:"calls"`
	WallNS int64  `json:"wall_ns"`
}

// Recorder collects the record of one run.
type Recorder struct {
	mu         sync.Mutex
	start      time.Time
	startAlloc uint64
	phase      Phase     // the phase running now
	phaseStart time.Time // when it started
	phaseAlloc uint64    // heap allocated when it started
	phaseCPU   *int64    // process CPU time when it started; nil if unknown
	phases     []Phase
	parse      Parse
	rules      map[string]*Rule
	files      int64
	bytes      int64
	timedOut   bool
}

// New starts a recorder. Its first phase is "run", which lasts until the
// first call of Phase.
func New() *Recorder {
	now, alloc := time.Now(), heapAlloc()
	return &Recorder{start: now, startAlloc: alloc, phase: Phase{Name: "run"}, phaseStart: now, phaseAlloc: alloc,
		phaseCPU: cpuTime(), rules: map[string]*Rule{}}
}

// Phase ends the phase running now and starts the named one.
func (r *Recorder) Phase(name string) {
	if r == nil {
		return
	}
	now, alloc, cpu := time.Now(), heapAlloc(), cpuTime()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endPhase(now, alloc, cpu)
	r.phase, r.phaseStart, r.phaseAlloc, r.phaseCPU = Phase{Name: name}, now, alloc, cpu
}

// endPhase appends the phase running now. r.mu must be held.
func (r *Recorder) endPhase(now time.Time, alloc uint64, cpu *int64) {
	p := r.phase
	p.WallNS = now.Sub(r.phaseStart).Nanoseconds()
	p.HeapAllocBytes = alloc - min(alloc, r.phaseAlloc)
	if cpu != nil && r.phaseCPU != nil {
		d := max(*cpu-*r.phaseCPU, 0)
		p.CPUNS = &d
	}
	r.phases = append(r.phases, p)
}

// Parse starts the timer of one parse and returns the function that stops
// it with the number of files and bytes parsed.
func (r *Recorder) Parse() func(files int, bytes int64) {
	if r == nil {
		return func(int, int64) {}
	}
	start := time.Now()
	return func(files int, bytes int64) {
		d := time.Since(start).Nanoseconds()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.parse.Calls++
		r.parse.Files += int64(files)
		r.parse.Bytes += bytes
		r.parse.WallNS += d
	}
}

// Rule starts the timer of one evaluation of rule id and returns the
// function that stops it.
func (r *Recorder) Rule(id string) func() {
	if r == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		d := time.Since(start).Nanoseconds()
		r.mu.Lock()
		defer r.mu.Unlock()
		e := r.rules[id]
		if e == nil {
			e = &Rule{ID: id}
			r.rules[id] = e
		}
		e.Calls++
		e.WallNS += d
	}
}

// Read counts files regular files and bytes bytes read from an input tree.
func (r *Recorder) Read(files int, bytes int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files += int64(files)
	r.bytes += bytes
}

// Timeout marks the run as ended by the command's timeout.
func (r *Recorder) Timeout() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timedOut = true
}

// Record ends the phase running now and returns a copy of the record of
// command, which exited with code. It returns nil for a nil recorder. A
// worker may go on recording after it; the copy does not change.
func (r *Recorder) Record(command, version string, code int) *Record {
	if r == nil {
		return nil
	}
	now, alloc, cpu := time.Now(), heapAlloc(), cpuTime()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endPhase(now, alloc, cpu)
	r.phase, r.phaseStart, r.phaseAlloc, r.phaseCPU = Phase{Name: "after_record"}, now, alloc, cpu
	rec := &Record{
		SchemaVersion:  SchemaVersion,
		Tool:           Tool{Name: "mtf-facts", Version: version},
		Command:        command,
		Outcome:        OutcomeCompleted,
		ExitCode:       code,
		StartedUnixNS:  r.start.UnixNano(),
		WallNS:         now.Sub(r.start).Nanoseconds(),
		HeapAllocBytes: alloc - min(alloc, r.startAlloc),
		FilesRead:      r.files,
		BytesRead:      r.bytes,
		Phases:         slices.Clone(r.phases),
		Parse:          r.parse,
		Rules:          []Rule{},
	}
	switch {
	case r.timedOut:
		rec.Outcome = OutcomeTimeout
	case code == exitError:
		rec.Outcome = OutcomeError
	}
	for _, id := range slices.Sorted(maps.Keys(r.rules)) {
		rec.Rules = append(rec.Rules, *r.rules[id])
	}
	rec.CPUUserNS, rec.CPUSystemNS, rec.MaxRSSBytes = usage()
	return rec
}

// heapAlloc reads the bytes the Go heap has allocated since the process
// started, 0 when the runtime does not report it.
func heapAlloc() uint64 {
	s := []metrics.Sample{{Name: allocMetric}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}
