package api

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a Zabbix server (API) version such as 7.4.16.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

// MinSupported and Tested are the oldest version zbxctl works with and the
// newest one it has been tried against. Newer servers get a warning, older ones
// an error.
var (
	MinSupported = Version{Major: 7, Minor: 4, Raw: "7.4"}
	Tested       = Version{Major: 7, Minor: 4, Raw: "7.4"}
)

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion reads "7.4", "7.4.16", "8.0.0alpha1" and similar.
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("cannot read the Zabbix version %q (expected something like 7.4 or 7.4.16)", s)
	}
	v := Version{Raw: s}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// IsZero reports an unknown version.
func (v Version) IsZero() bool { return v.Major == 0 && v.Minor == 0 }

// AtLeast reports whether v is major.minor or newer. Commands use it to pick
// the request shape a server understands.
func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// UnsupportedVersionError is returned for a server older than MinSupported.
type UnsupportedVersionError struct{ Have Version }

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("this Zabbix server is %s; zbxctl supports %s and newer (set ZBXCTL_ALLOW_UNSUPPORTED=1 or --allow-unsupported to try anyway)", e.Have, MinSupported)
}

// Check says whether the version can be used: an error for too old, and
// newer=true for a version newer than the one zbxctl was tested against.
func (v Version) Check() (newer bool, err error) {
	if v.IsZero() {
		return false, nil
	}
	if !v.AtLeast(MinSupported.Major, MinSupported.Minor) {
		return false, &UnsupportedVersionError{Have: v}
	}
	return v.Major > Tested.Major || (v.Major == Tested.Major && v.Minor > Tested.Minor), nil
}
