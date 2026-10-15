package state

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func numbersIn(t *testing.T) Numbers {
	t.Helper()
	return Numbers{Dir: filepath.Join(t.TempDir(), "sub", "numbers")}
}

func TestNumbersRoundTrip(t *testing.T) {
	n := numbersIn(t)
	if err := n.Save("list1", []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	got, err := n.Load("list1")
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestNumbersPerListIsolation(t *testing.T) {
	n := numbersIn(t)
	_ = n.Save("L1", []string{"a"})
	_ = n.Save("L2", []string{"x", "y"})
	if got, _ := n.Load("L1"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("L1 = %v", got)
	}
	if got, _ := n.Load("L2"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("L2 = %v", got)
	}
	_ = n.Save("L1", []string{"z"})
	if got, _ := n.Load("L1"); !reflect.DeepEqual(got, []string{"z"}) {
		t.Errorf("replace L1 = %v", got)
	}
	if got, _ := n.Load("L2"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("L2 disturbed: %v", got)
	}
	if got, err := n.Load("never-saved"); err != nil || len(got) != 0 {
		t.Errorf("unknown list = %v, %v", got, err)
	}
}

func TestNumbersMissingFile(t *testing.T) {
	got, err := numbersIn(t).Load("L")
	if err != nil || got != nil {
		t.Errorf("got %v, %v; want nil, nil", got, err)
	}
}

func TestNumbersCorruptFile(t *testing.T) {
	n := numbersIn(t)
	if err := n.Save("L", []string{"seed"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(n.Dir)
	if len(entries) != 1 {
		t.Fatalf("fixture: %d files", len(entries))
	}
	file := filepath.Join(n.Dir, entries[0].Name())
	for _, c := range []string{"{not json", "", "\x00\xff", `{"L":["a"]}`, `"str"`} {
		if err := os.WriteFile(file, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := n.Load("L")
		if err != nil || len(got) != 0 {
			t.Errorf("content %q: Load = %v, %v; want nil, nil", c, got, err)
		}
		if err := n.Save("L", []string{"a"}); err != nil {
			t.Errorf("content %q: Save failed: %v", c, err)
		}
		if got, _ := n.Load("L"); !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("content %q: after Save, Load = %v", c, got)
		}
	}
}

func TestNumbersForget(t *testing.T) {
	n := numbersIn(t)
	if err := n.Forget("L"); err != nil {
		t.Errorf("forget with nothing saved: %v", err)
	}
	_ = n.Save("L", []string{"a"})
	_ = n.Save("M", []string{"b"})
	if err := n.Forget("L"); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Load("L"); len(got) != 0 {
		t.Errorf("L after forget = %v", got)
	}
	if got, _ := n.Load("M"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("M disturbed: %v", got)
	}
}

func TestNumbersZeroDirDisabled(t *testing.T) {
	var n Numbers
	if got, err := n.Load("L"); got != nil || err != nil {
		t.Errorf("Load = %v, %v", got, err)
	}
	if err := n.Save("L", []string{"a"}); err == nil {
		t.Error("Save with no directory should error")
	}
	if err := n.Forget("L"); err != nil {
		t.Errorf("Forget = %v", err)
	}
}

func TestNumbersOddListIDs(t *testing.T) {
	n := numbersIn(t)
	for i, id := range []string{"../../etc/passwd", "a/b", "", "日本語", strings.Repeat("x", 10000), `C:\x`} {
		want := []string{fmt.Sprint(i)}
		if err := n.Save(id, want); err != nil {
			t.Fatalf("Save(%.20q): %v", id, err)
		}
		if got, _ := n.Load(id); !reflect.DeepEqual(got, want) {
			t.Errorf("Load(%.20q) = %v", id, got)
		}
	}
	// Nothing escaped Dir.
	parent, _ := os.ReadDir(filepath.Dir(n.Dir))
	if len(parent) != 1 {
		t.Errorf("files outside Dir: %v", parent)
	}
}

func TestNumbersConcurrentSavesForDifferentListsBothPersist(t *testing.T) {
	for round := 0; round < 20; round++ {
		n := numbersIn(t)
		var wg sync.WaitGroup
		for _, id := range []string{"A", "B", "C", "D"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := n.Save(id, []string{id + "1", id + "2"}); err != nil {
					t.Errorf("Save(%s): %v", id, err)
				}
			}()
		}
		wg.Wait()
		for _, id := range []string{"A", "B", "C", "D"} {
			if got, _ := n.Load(id); !reflect.DeepEqual(got, []string{id + "1", id + "2"}) {
				t.Fatalf("round %d: list %s = %v", round, id, got)
			}
		}
	}
}

func TestNumbersSaveEmptyAndLarge(t *testing.T) {
	n := numbersIn(t)
	if err := n.Save("L", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := n.Load("L"); err != nil || len(got) != 0 {
		t.Errorf("empty: %v, %v", got, err)
	}
	ids := make([]string, 50000)
	for i := range ids {
		ids[i] = "id" + string(rune('a'+i%26))
	}
	if err := n.Save("L", ids); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Load("L"); !reflect.DeepEqual(got, ids) {
		t.Error("large numbering did not round-trip")
	}
}

func TestDefaultNumbersHonoursCacheDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTASKS_CACHE_DIR", dir)
	n, err := DefaultNumbers()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Save("L", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "numbers")); err != nil {
		t.Errorf("numbers dir not under GTASKS_CACHE_DIR: %v", err)
	}
}

func TestDefaultNumbersErrorsWithoutCacheDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cache dir resolution fails only on linux with HOME and XDG_CACHE_HOME unset")
	}
	t.Setenv("GTASKS_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	n, err := DefaultNumbers()
	if err == nil || n.Dir != "" {
		t.Errorf("got %+v, %v; want error and no directory (no temp-dir fallback)", n, err)
	}
}
