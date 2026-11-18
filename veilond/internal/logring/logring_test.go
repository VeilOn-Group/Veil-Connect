package logring

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationBoundsSize(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 500; i++ {
		fmt.Fprintf(r, "line %04d some padding text\n", i)
	}
	var total int64
	for _, n := range []string{curName, prevName} {
		st, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > 1024 {
			t.Fatalf("%s exceeds cap: %d", n, st.Size())
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s perms %v, want 0600", n, st.Mode().Perm())
		}
		total += st.Size()
	}
	lines, _ := r.Tail(3)
	if len(lines) != 3 || !strings.Contains(lines[2], "line 0499") {
		t.Fatalf("tail wrong: %v", lines)
	}
}

func TestWipe(t *testing.T) {
	dir := t.TempDir()
	r, _ := Open(dir, 1024)
	defer r.Close()
	fmt.Fprintln(r, "something")
	if err := r.Wipe(); err != nil {
		t.Fatal(err)
	}
	lines, _ := r.Tail(10)
	if len(lines) != 0 {
		t.Fatalf("wipe left %v", lines)
	}
	fmt.Fprintln(r, "after")
	if lines, _ := r.Tail(10); len(lines) != 1 {
		t.Fatal("ring unusable after wipe")
	}
}
