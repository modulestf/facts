package facts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/modulestf/facts/internal/profile"
)

// profileLimits bound the read of a profile directory. A profile is a few
// markdown files and templates, far below these.
var profileLimits = Limits{MaxFiles: 1000, MaxFileBytes: 256 << 10, MaxTotalBytes: 4 << 20}

// noProfile is the reason a rule that needs the profile gives when the run
// was given none.
const noProfile = "no profile input"

// applyProfile reads the module profile in Options.ProfileDir into every
// area and returns the profile's SHA-256 for the envelope, nil when the run
// read no valid profile.
//
// A ProfileDir that is not a directory, a link to one included, is an error:
// the caller named an input that is not there. A directory whose profile
// cannot be read or parsed is not: every area gets the reason instead, and the
// rules that need the profile are not_evaluated(profile_unavailable).
func applyProfile(o Options, areas []*Area) (*string, error) {
	issue := noProfile
	var p *profile.Profile
	if o.ProfileDir != "" {
		st, err := os.Lstat(o.ProfileDir)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("profile: %s is not a directory", o.ProfileDir)
		}
		lim := profileLimits
		lim.Deadline = o.Limits.Deadline
		if p, err = readProfile(o.ProfileDir, lim); errors.Is(err, ErrDeadline) {
			return nil, err
		} else if err != nil {
			issue = "profile: " + err.Error()
		}
	}
	for _, a := range areas {
		a.Profile, a.profileIssue = p, issue
	}
	if p == nil {
		return nil, nil
	}
	return &p.SHA256, nil
}

// readProfile reads dir with the tree reader, so it follows no link and caps
// every file, and builds the profile from its regular files. PROFILE.md or a
// template the profile names that is a link, special, unreadable or over the
// cap is an error.
func readProfile(dir string, lim Limits) (*profile.Profile, error) {
	t, err := ReadTree(dir, lim)
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		// A path error names the host path; the reason must not.
		return nil, errors.New("profile directory unreadable")
	} else if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range t.Entries {
		if e.Kind == KindFile {
			files[e.Path] = e.Data
		}
	}
	p, err := profile.New(files)
	if err != nil {
		// Say why a file the profile needs was not read: a link, special,
		// unreadable or over the cap.
		for _, q := range t.Paths {
			if e := t.Entries[q]; e.Kind != KindFile && (q == profile.File || q == profile.TemplateDir || path.Dir(q) == profile.TemplateDir) {
				err = fmt.Errorf("%w; %s is %s", err, q, e.Kind)
			}
		}
		return nil, err
	}
	return p, nil
}

// needProfile is the first step of a rule that reads the profile. ok is false
// when the area has none, and res is then the rule's not_evaluated result.
func needProfile(a *Area) (res Result, ok bool) {
	if a.Profile != nil {
		return Result{}, true
	}
	return Result{NotEvaluated: a.profileIssue, Code: ReasonProfileUnavailable}, false
}
