// Package cfgbackup snapshots podkop's UCI config file alongside the original
// (in /etc/config) with a "<version>-<timestamp>" suffix, so a downgrade or a
// broken update can restore a matching configuration. Backups are named
// "<base>.bak-<version>-<YYYYMMDD-HHMMSS>" — the dot makes UCI ignore them
// (verified: uci show skips dotted files), so they sit next to the live config
// without polluting the UCI namespace. Multiple backups per version are kept,
// distinguished by timestamp.
package cfgbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultConfigPath is podkop's UCI config file.
const DefaultConfigPath = "/etc/config/podkop"

// StampLayout is the timestamp format used in backup IDs.
const StampLayout = "20060102-150405"

const bakInfix = ".bak-"

var (
	versionRE = regexp.MustCompile(`^[0-9A-Za-z._]+$`)    // no '-': it separates version from stamp
	stampRE   = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`) // YYYYMMDD-HHMMSS
	idRE      = regexp.MustCompile(`^(.+)-([0-9]{8}-[0-9]{6})$`)
)

// Entry identifies a single backup: its podkop version, its timestamp, and the
// ID (the "<version>-<stamp>" suffix used in the filename and callbacks).
type Entry struct {
	Version string
	Stamp   string // raw, StampLayout
	ID      string // "<version>-<stamp>"
}

// Display renders the timestamp as "2006-01-02 15:04:05" for the UI.
func (e Entry) Display() string {
	if t, err := time.Parse(StampLayout, e.Stamp); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return e.Stamp
}

// Parse splits an ID into an Entry; ok is false if the ID is malformed.
func Parse(id string) (Entry, bool) {
	m := idRE.FindStringSubmatch(id)
	if m == nil || !versionRE.MatchString(m[1]) {
		return Entry{}, false
	}
	return Entry{Version: m[1], Stamp: m[2], ID: id}, true
}

// Store performs versioned backup/restore of a single config file.
type Store struct {
	configPath string
}

// New returns a Store for configPath (empty → DefaultConfigPath).
func New(configPath string) *Store {
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	return &Store{configPath: configPath}
}

// ConfigPath returns the live config file path.
func (s *Store) ConfigPath() string { return s.configPath }

func (s *Store) pathForID(id string) string {
	base := filepath.Base(s.configPath)
	return filepath.Join(filepath.Dir(s.configPath), base+bakInfix+id)
}

// Backup copies the live config to "<base>.bak-<version>-<stamp>" and returns
// the backup path. Empty stamp defaults to now.
func (s *Store) Backup(version, stamp string) (string, error) {
	if !versionRE.MatchString(version) {
		return "", fmt.Errorf("cfgbackup: invalid version %q", version)
	}
	if stamp == "" {
		stamp = time.Now().Format(StampLayout)
	}
	if !stampRE.MatchString(stamp) {
		return "", fmt.Errorf("cfgbackup: invalid stamp %q", stamp)
	}
	dst := s.pathForID(version + "-" + stamp)
	if err := copyFile(s.configPath, dst); err != nil {
		return "", fmt.Errorf("cfgbackup: backup %s: %w", version, err)
	}
	return dst, nil
}

// RestoreID copies the backup identified by id over the live config.
func (s *Store) RestoreID(id string) error {
	if _, ok := Parse(id); !ok {
		return fmt.Errorf("cfgbackup: invalid backup id %q", id)
	}
	src := s.pathForID(id)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("cfgbackup: no backup %s", id)
	}
	if err := copyFile(src, s.configPath); err != nil {
		return fmt.Errorf("cfgbackup: restore %s: %w", id, err)
	}
	return nil
}

// Delete removes the backup identified by id.
func (s *Store) Delete(id string) error {
	if _, ok := Parse(id); !ok {
		return fmt.Errorf("cfgbackup: invalid backup id %q", id)
	}
	p := s.pathForID(id)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("cfgbackup: no backup %s", id)
	}
	return os.Remove(p)
}

// List returns every backup entry, newest timestamp first.
func (s *Store) List() ([]Entry, error) {
	dir := filepath.Dir(s.configPath)
	prefix := filepath.Base(s.configPath) + bakInfix
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cfgbackup: list: %w", err)
	}
	var out []Entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		id := strings.TrimPrefix(e.Name(), prefix)
		if ent, ok := Parse(id); ok {
			out = append(out, ent)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stamp > out[j].Stamp })
	return out, nil
}

// Versions returns the distinct versions that have at least one backup,
// newest-version first.
func (s *Store) Versions() ([]string, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var vs []string
	for _, e := range all {
		if !seen[e.Version] {
			seen[e.Version] = true
			vs = append(vs, e.Version)
		}
	}
	sort.Sort(sort.Reverse(byVersion(vs)))
	return vs, nil
}

// ForVersion returns the backups for a specific version, newest first.
func (s *Store) ForVersion(version string) ([]Entry, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range all {
		if e.Version == version {
			out = append(out, e)
		}
	}
	return out, nil
}

// HasVersion reports whether any backup exists for version.
func (s *Store) HasVersion(version string) bool {
	vs, err := s.ForVersion(version)
	return err == nil && len(vs) > 0
}

// LatestIDForVersion returns the newest backup ID for version.
func (s *Store) LatestIDForVersion(version string) (string, bool) {
	vs, err := s.ForVersion(version)
	if err != nil || len(vs) == 0 {
		return "", false
	}
	return vs[0].ID, true
}

// copyFile copies src to dst, preserving 0600 (config may hold secrets).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// byVersion sorts dotted numeric versions ascending; non-numeric segments fall
// back to string compare.
type byVersion []string

func (b byVersion) Len() int      { return len(b) }
func (b byVersion) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byVersion) Less(i, j int) bool {
	ai := strings.Split(b[i], ".")
	aj := strings.Split(b[j], ".")
	for k := 0; k < len(ai) && k < len(aj); k++ {
		if ai[k] == aj[k] {
			continue
		}
		ni, ei := atoi(ai[k])
		nj, ej := atoi(aj[k])
		if ei == nil && ej == nil {
			return ni < nj
		}
		return ai[k] < aj[k]
	}
	return len(ai) < len(aj)
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("nan")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
