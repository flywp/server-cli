package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// cpuTimes are the CPU counters of /proc/stat, in clock ticks.
type cpuTimes struct {
	Idle  uint64 `json:"idle"`
	Total uint64 `json:"total"`
}

// parseCPU reads the first line of /proc/stat:
//
//	cpu  user nice system idle iowait irq softirq steal guest guest_nice
//
// Idle is idle + iowait. Total leaves out guest and guest_nice, because user
// and nice already hold them.
func parseCPU(data []byte) (cpuTimes, error) {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("/proc/stat: unexpected first line %q", line)
	}

	var v [8]uint64
	for i := 0; i < len(v) && i+1 < len(fields); i++ {
		n, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("/proc/stat: %w", err)
		}
		v[i] = n
	}

	var t cpuTimes
	for _, n := range v {
		t.Total += n
	}
	t.Idle = v[3] + v[4]

	return t, nil
}

// cpuPercent is the busy share of the time between two readings, 0 to 100.
func cpuPercent(prev, cur cpuTimes) float64 {
	if cur.Total <= prev.Total || cur.Idle < prev.Idle {
		return 0
	}

	total := float64(cur.Total - prev.Total)
	idle := float64(cur.Idle - prev.Idle)
	return min(max((total-idle)/total*100, 0), 100)
}

// parseLoad reads the 1 minute load average from /proc/loadavg.
func parseLoad(data []byte) (float64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("/proc/loadavg is empty")
	}

	return strconv.ParseFloat(fields[0], 64)
}

// memory holds the values of /proc/meminfo, in bytes.
type memory struct {
	total, available, swapTotal, swapFree uint64
}

func parseMeminfo(data []byte) (memory, error) {
	values := map[string]uint64{}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		key, rest, ok := strings.Cut(s.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		// The kernel shows these values in kB (KiB).
		values[key] = n * 1024
	}

	for _, key := range []string{"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"} {
		if _, ok := values[key]; !ok {
			return memory{}, fmt.Errorf("/proc/meminfo has no %s", key)
		}
	}

	return memory{
		total:     values["MemTotal"],
		available: values["MemAvailable"],
		swapTotal: values["SwapTotal"],
		swapFree:  values["SwapFree"],
	}, nil
}

// netCounters are the received and sent bytes of one interface.
type netCounters struct {
	In  uint64 `json:"in"`
	Out uint64 `json:"out"`
}

// parseNetDev reads /proc/net/dev. Each interface line is
//
//	name: rx_bytes rx_packets ... (8 receive fields) tx_bytes ...
func parseNetDev(data []byte) (map[string]netCounters, error) {
	out := map[string]netCounters{}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		name, rest, ok := strings.Cut(s.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		in, err1 := strconv.ParseUint(fields[0], 10, 64)
		sent, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("/proc/net/dev: bad counters for %s", strings.TrimSpace(name))
		}
		out[strings.TrimSpace(name)] = netCounters{In: in, Out: sent}
	}

	return out, nil
}

// parseDefaultRoute returns the interface of the default route in
// /proc/net/route, or "". The default route has destination and mask 0: a
// VPN that routes 0.0.0.0/1 and 128.0.0.0/1 is not the default route.
//
//	Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
func parseDefaultRoute(data []byte) string {
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) >= 8 && fields[1] == "00000000" && fields[7] == "00000000" {
			return fields[0]
		}
	}

	return ""
}

// parseOSRelease returns PRETTY_NAME from /etc/os-release.
func parseOSRelease(data []byte) string {
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "PRETTY_NAME="); ok {
			if unquoted, err := strconv.Unquote(v); err == nil {
				return unquoted
			}
			return strings.Trim(v, `"'`)
		}
	}

	return ""
}

// parseUptime reads the seconds since boot from /proc/uptime.
func parseUptime(data []byte) (uint64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("/proc/uptime is empty")
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("/proc/uptime: bad value %q", fields[0])
	}

	return uint64(f), nil
}

// parseAptCheck reads the output of apt-check: "total;security". apt-check
// can write warnings before the result (on Ubuntu 24.04, for example for a
// source that is configured two times), so only the last line counts.
func parseAptCheck(out []byte) (total, security uint64, err error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	a, b, ok := strings.Cut(last, ";")
	if !ok {
		return 0, 0, fmt.Errorf("apt-check: unexpected output %q", last)
	}
	if total, err = strconv.ParseUint(a, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("apt-check: %w", err)
	}
	if security, err = strconv.ParseUint(b, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("apt-check: %w", err)
	}

	return total, security, nil
}
