package metrics

import (
	"bufio"
	"cmp"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/statefile"
)

const (
	// trafficLimit is the size past which the agent cuts the traffic log,
	// once it has read all of it (contract v0.6.0).
	trafficLimit = 64 << 20
	// trafficMaxHosts is the most names in a sample: past it, the names with
	// the fewest bytes are left out (contract v0.6.0).
	trafficMaxHosts = 1000
)

// trafficReader reads the traffic log that the server's front proxy writes
// for the agent (contract v0.6.0, "Traffic and own files"): one line for each
// request, "$server_name $bytes_sent". The offset of the next line to read is
// kept in a state file, so a restart reads on from where it stopped.
type trafficReader struct {
	path  string // the log, under the home folder; "" without a home
	state string // traffic.json
	limit int64  // trafficLimit; tests set a smaller one
}

// trafficOffset is where the next read starts, in the file with that inode.
type trafficOffset struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

// read returns the bytes the proxy sent for each name since the last read,
// most first. It returns nil when the server has no traffic log, and an
// empty, non-nil slice when no request came. The new offset is saved before
// it returns: a stop before the sample is buffered loses those bytes, and
// never sends them two times.
func (r *trafficReader) read(log *slog.Logger) []wire.Traffic {
	if r.path == "" {
		return nil
	}
	// Never through a symbolic link, and only a regular file: the agent cuts
	// this file, so a link in its place must not make it cut another one.
	f, err := openNoFollow(r.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("cannot read the traffic log", "error", err)
		}
		return nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		log.Warn("cannot read the traffic log: not a regular file", "error", err)
		return nil
	}
	size, inode := info.Size(), inodeOf(info)

	var saved trafficOffset
	if err := statefile.Read(r.state, &saved); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("ignoring the saved traffic offset", "error", err)
		}
		// A first read starts at the end: the lines before it can be of an
		// earlier month, and one sample must not bring them all.
		r.save(log, trafficOffset{Inode: inode, Offset: size})
		return []wire.Traffic{}
	}

	offset := saved.Offset
	if saved.Inode != inode || size < offset {
		// A new file, or one that was cut: from the start.
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		log.Warn("cannot read the traffic log", "error", err)
		return nil
	}

	sent := map[string]uint64{}
	lines := bufio.NewReader(io.LimitReader(f, size-offset))
	for {
		line, err := lines.ReadString('\n')
		if err != nil {
			// The end, or a line that is still being written: it waits for
			// the next read.
			break
		}
		offset += int64(len(line))
		host, bytes, ok := strings.Cut(strings.TrimSpace(line), " ")
		n, err := strconv.ParseUint(bytes, 10, 64)
		if !ok || err != nil || host == "" || len(host) > 255 {
			continue
		}
		sent[host] += n
	}

	// Read to the end of a large file: cut it, so that it stays small. The
	// lines written between the read and the cut are lost.
	if offset == size && size > r.limit {
		if err := f.Truncate(0); err != nil {
			log.Warn("cannot cut the traffic log", "error", err)
		} else {
			offset = 0
		}
	}
	r.save(log, trafficOffset{Inode: inode, Offset: offset})

	hosts := slices.SortedFunc(maps.Keys(sent), func(a, b string) int {
		return cmp.Or(cmp.Compare(sent[b], sent[a]), cmp.Compare(a, b))
	})
	out := make([]wire.Traffic, 0, min(len(hosts), trafficMaxHosts))
	for _, host := range hosts[:min(len(hosts), trafficMaxHosts)] {
		out = append(out, wire.Traffic{Host: host, Bytes: sent[host]})
	}
	return out
}

func (r *trafficReader) save(log *slog.Logger, o trafficOffset) {
	if err := statefile.Write(r.state, o); err != nil {
		log.Warn("saving the traffic offset", "error", err)
	}
}
