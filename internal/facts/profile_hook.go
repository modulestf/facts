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
// area and returns the profile's SHA-256 and the tree hash of the directory
// for the envelope, both nil when the run read no valid profile. The tree
// hash is also nil when the directory holds an entry that is not a regular
// file.
//
// A ProfileDir that is not a directory, a link to one included, is an error:
// the caller named an input that is not there. A directory whose profile
// cannot be read or parsed is not: every area gets the reason instead, and the
// rules that need the profile are not_evaluated(profile_unavailable).
func applyProfile(o Options, areas []*Area) (sum, tree *string, err error) {
	issue := noProfile
	var p *profile.Profile
	var treeSum string
	if o.ProfileDir != "" {
		st, err := os.Lstat(o.ProfileDir)
		if err != nil || !st.IsDir() {
			return nil, nil, fmt.Errorf("profile: %s is not a directory", o.ProfileDir)
		}
		lim := profileLimits
		lim.Deadline = o.Limits.Deadline
		if p, treeSum, err = readProfile(o.ProfileDir, lim); errors.Is(err, ErrDeadline) {
			return nil, nil, err
		} else if err != nil {
			issue = "profile: " + err.Error()
		}
	}
	for _, a := range areas {
		a.Profile, a.profileIssue = p, issue
	}
	if p == nil {
		return nil, nil, nil
	}
	if treeSum != "" {
		tree = &treeSum
	}
	return &p.SHA256, tree, nil
}

// ErrProfileUnreadable is the error of a profile directory the tree reader
// cannot read: a link, over a read limit, or gone. Its text names no path.
var ErrProfileUnreadable = errors.New("profile directory unreadable")

// ReadProfile reads and parses the profile in dir as lint does, with the
// profile read limits. unreadable is set for a directory that cannot be read
// at all, which is ErrProfileUnreadable or ErrDeadline; invalid is set for one
// that reads but is not a valid profile.
func ReadProfile(dir string) (p *profile.Profile, unreadable, invalid error) {
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return nil, ErrProfileUnreadable, nil
	}
	t, err := readProfileTree(dir, profileLimits)
	if err != nil {
		return nil, err, nil
	}
	p, _, err = profileFromTree(t)
	return p, nil, err
}

// readProfile reads dir with the tree reader, so it follows no link and caps
// every file, and builds the profile from its regular files. PROFILE.md or a
// template the profile names that is a link, special, unreadable or over the
// cap is an error. treeSum hashes every file as profile.TreeSHA256 does, and
// is "" when an entry is not a regular file.
func readProfile(dir string, lim Limits) (p *profile.Profile, treeSum string, err error) {
	t, err := readProfileTree(dir, lim)
	if err != nil {
		return nil, "", err
	}
	return profileFromTree(t)
}

// readProfileTree reads dir with the tree reader.
func readProfileTree(dir string, lim Limits) (*Tree, error) {
	t, err := ReadTree(dir, lim)
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		// A path error names the host path; the reason must not.
		return nil, ErrProfileUnreadable
	}
	return t, err
}

// profileFromTree builds the profile from the regular files of t.
func profileFromTree(t *Tree) (p *profile.Profile, treeSum string, err error) {
	files := map[string][]byte{}
	regular := true
	for _, e := range t.Entries {
		if e.Kind == KindFile {
			files[e.Path] = e.Data
		} else {
			regular = false
		}
		if !profile.ValidName(e.Path) {
			regular = false // a name the tree hash cannot carry unambiguously
		}
	}
	if regular {
		treeSum = profile.TreeSHA256(files)
	}
	p, err = profile.New(files)
	if err != nil {
		// Say why a file the profile needs was not read: a link, special,
		// unreadable or over the cap.
		for _, q := range t.Paths {
			if e := t.Entries[q]; e.Kind != KindFile && (q == profile.File || q == profile.TemplateDir || path.Dir(q) == profile.TemplateDir) {
				err = fmt.Errorf("%w; %s is %s", err, q, e.Kind)
			}
		}
		return nil, "", err
	}
	return p, treeSum, nil
}

// needProfile is the first step of a rule that reads the profile. ok is false
// when the area has none, and res is then the rule's not_evaluated result.
func needProfile(a *Area) (res Result, ok bool) {
	if a.Profile != nil {
		return Result{}, true
	}
	return Result{NotEvaluated: a.profileIssue, Code: ReasonProfileUnavailable}, false
}
