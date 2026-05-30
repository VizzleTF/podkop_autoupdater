package cfgbackup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBackupVersionsStampsRestore(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "podkop")
	write := func(s string) {
		if err := os.WriteFile(cfg, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := New(cfg)

	write("cfg-A")
	if _, err := s.Backup("0.7.18", "20260530-100000"); err != nil {
		t.Fatal(err)
	}
	write("cfg-B")
	if _, err := s.Backup("0.7.18", "20260530-120000"); err != nil {
		t.Fatal(err)
	}
	write("cfg-C")
	if _, err := s.Backup("0.7.9", "20260530-110000"); err != nil {
		t.Fatal(err)
	}

	// Distinct versions, newest version first.
	vs, err := s.Versions()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vs, []string{"0.7.18", "0.7.9"}) {
		t.Fatalf("versions: %v", vs)
	}

	// Stamps for a version, newest first.
	ents, _ := s.ForVersion("0.7.18")
	if len(ents) != 2 || ents[0].Stamp != "20260530-120000" {
		t.Fatalf("forVersion order: %+v", ents)
	}
	if ents[0].Display() != "2026-05-30 12:00:00" {
		t.Fatalf("display: %s", ents[0].Display())
	}

	// Latest-for-version and restore.
	id, ok := s.LatestIDForVersion("0.7.18")
	if !ok || id != "0.7.18-20260530-120000" {
		t.Fatalf("latest id: %s ok=%v", id, ok)
	}
	if err := s.RestoreID("0.7.18-20260530-100000"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "cfg-A" {
		t.Fatalf("restored content: %q", b)
	}

	if !s.HasVersion("0.7.9") || s.HasVersion("9.9.9") {
		t.Fatal("HasVersion wrong")
	}
	if err := s.RestoreID("0.7.18-29990101-000000"); err == nil {
		t.Fatal("restore of missing backup should error")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "podkop")
	if err := os.WriteFile(cfg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(cfg)
	stamps := []string{"20260530-100000", "20260530-110000", "20260530-120000", "20260530-130000"}
	for _, st := range stamps {
		if _, err := s.Backup("0.7.18", st); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := s.Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d want 2", removed)
	}
	ents, _ := s.List()
	if len(ents) != 2 || ents[0].Stamp != "20260530-130000" || ents[1].Stamp != "20260530-120000" {
		t.Fatalf("kept wrong newest: %+v", ents)
	}
	// keep=0 → unlimited, no-op.
	if n, _ := s.Prune(0); n != 0 {
		t.Fatalf("prune(0) removed %d", n)
	}
}

func TestInvalidInputs(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "podkop"))
	if _, err := s.Backup("../etc/passwd", "20260530-100000"); err == nil {
		t.Fatal("bad version must be rejected")
	}
	if _, err := s.Backup("0.7.18", "nonsense"); err == nil {
		t.Fatal("bad stamp must be rejected")
	}
	if _, ok := Parse("0.7.18-20260530-120000"); !ok {
		t.Fatal("valid id must parse")
	}
	if _, ok := Parse("garbage"); ok {
		t.Fatal("garbage id must not parse")
	}
}
