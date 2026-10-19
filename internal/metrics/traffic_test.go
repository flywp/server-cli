package metrics

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// trafficLog returns a reader of a traffic log in a temporary folder, and a
// function that appends lines to the log.
func trafficLog(t *testing.T) (*trafficReader, func(string)) {
	t.Helper()
	dir := t.TempDir()
	r := &trafficReader{path: filepath.Join(dir, "traffic.log"), state: filepath.Join(dir, "traffic.json"), limit: trafficLimit}
	write := func(lines string) {
		f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o664)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(lines); err != nil {
			t.Fatal(err)
		}
	}
	return r, write
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestTrafficWithNoLogIsNull(t *testing.T) {
	r, _ := trafficLog(t)
	if got := r.read(quiet); got != nil {
		t.Errorf("read() = %v, want nil: no traffic log", got)
	}
	if got := (&trafficReader{}).read(quiet); got != nil {
		t.Errorf("read() with no home = %v, want nil", got)
	}
}

func TestTrafficStartsAtTheEndThenAddsEachName(t *testing.T) {
	r, write := trafficLog(t)
	// Written before the agent read the log: of an earlier month, perhaps.
	write("old.example.com 999999\n")

	if got := r.read(quiet); got == nil || len(got) != 0 {
		t.Fatalf("first read = %v, want an empty list: it starts at the end", got)
	}

	write("shop.example.com 100\n*.blog.example.com 40\nshop.example.com 250\nnot a line\nbad.example.com -3\n\nhalf.example.com 7")
	want := []wire.Traffic{{Host: "shop.example.com", Bytes: 350}, {Host: "*.blog.example.com", Bytes: 40}}
	if got := r.read(quiet); !reflect.DeepEqual(got, want) {
		t.Errorf("read() = %v, want %v: the lines since, by name, most first, bad lines skipped", got, want)
	}

	// The line that was still being written comes with the next read.
	write("1\n")
	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "half.example.com", Bytes: 71}}) {
		t.Errorf("read() = %v, want the finished line", got)
	}
	if got := r.read(quiet); got == nil || len(got) != 0 {
		t.Errorf("read() with no new line = %v, want an empty list", got)
	}
}

func TestTrafficReadsANewOrCutFileFromTheStart(t *testing.T) {
	r, write := trafficLog(t)
	write("a.example.com 1\n")
	r.read(quiet)
	write("a.example.com 10\n")
	r.read(quiet)

	// Cut by the cron line: shorter than the offset.
	if err := os.Truncate(r.path, 0); err != nil {
		t.Fatal(err)
	}
	write("a.example.com 5\n")
	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "a.example.com", Bytes: 5}}) {
		t.Errorf("read() after a cut = %v, want the new lines from the start", got)
	}

	// A new file in its place.
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	write("a.example.com 300000\n")
	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "a.example.com", Bytes: 300000}}) {
		t.Errorf("read() of a new file = %v, want it from the start", got)
	}
}

func TestTrafficKnowsANewFileWithTheSameInode(t *testing.T) {
	r, write := trafficLog(t)
	write("")
	r.read(quiet)
	write("shop.example.com 7\n")
	r.read(quiet)

	// Linux can give a new file the inode of one that was just deleted. The
	// same file written again has the same inode on each system.
	if err := os.WriteFile(r.path, []byte("a.shop.example.com 300000\n"), 0o664); err != nil {
		t.Fatal(err)
	}
	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "a.shop.example.com", Bytes: 300000}}) {
		t.Errorf("read() of a new file with the same inode = %v, want it from the start", got)
	}
}

func TestTrafficCutsALargeLogOnceItIsRead(t *testing.T) {
	r, write := trafficLog(t)
	r.limit = 64
	write("")
	r.read(quiet)
	write(strings.Repeat("big.example.com 2\n", 10))

	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "big.example.com", Bytes: 20}}) {
		t.Fatalf("read() = %v, want all of it", got)
	}
	if info, err := os.Stat(r.path); err != nil || info.Size() != 0 {
		t.Errorf("log after the read = %v, %v, want cut to 0", info, err)
	}
	write("big.example.com 3\n")
	if got := r.read(quiet); !reflect.DeepEqual(got, []wire.Traffic{{Host: "big.example.com", Bytes: 3}}) {
		t.Errorf("read() after the cut = %v, want the new line", got)
	}
}

func TestTrafficSendsTheNamesWithTheMostBytes(t *testing.T) {
	r, write := trafficLog(t)
	write("")
	r.read(quiet)
	var lines strings.Builder
	for i := range trafficMaxHosts + 5 {
		fmt.Fprintf(&lines, "site%d.example.com %d\n", i, i+1)
	}
	write(lines.String())

	got := r.read(quiet)
	if len(got) != trafficMaxHosts || got[0].Bytes != trafficMaxHosts+5 || got[len(got)-1].Bytes != 6 {
		t.Errorf("read() = %d names from %d to %d bytes, want the %d with the most", len(got), got[0].Bytes, got[len(got)-1].Bytes, trafficMaxHosts)
	}
}

func TestTrafficRefusesALinkInPlaceOfTheLog(t *testing.T) {
	r, _ := trafficLog(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte(strings.Repeat("x 1\n", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, r.path); err != nil {
		t.Fatal(err)
	}
	r.limit = 1

	if got := r.read(quiet); got != nil {
		t.Errorf("read() through a link = %v, want nil", got)
	}
	if info, err := os.Stat(other); err != nil || info.Size() != 400 {
		t.Errorf("the link's target = %v, %v, want it untouched", info, err)
	}
}
