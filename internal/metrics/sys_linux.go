package metrics

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// statfs returns the size and the used space of the file system that holds
// path. Used space is (blocks − free blocks): the space that is reserved for
// root is used space, because the sites cannot use it.
func statfs(path string) (total, used uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}

	// The block counts are in units of the fragment size (f_frsize), the
	// same as df uses.
	size := uint64(st.Frsize)
	if size == 0 {
		size = uint64(st.Bsize)
	}

	return st.Blocks * size, (st.Blocks - st.Bfree) * size, nil
}

// kernelRelease returns the kernel release, as "uname -r" shows it.
func kernelRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}

	return unix.ByteSliceToString(u.Release[:])
}

// lowerIOPriority gives the calling goroutine the idle I/O class: its disk
// reads wait for all other reads. The class is a property of the thread, so
// the goroutine stays locked to its thread, and the thread ends with the
// goroutine. Without the right to set it, the priority does not change.
func lowerIOPriority() {
	runtime.LockOSThread()

	const (
		whoProcess = 1 // IOPRIO_WHO_PROCESS: with id 0, the calling thread
		classIdle  = 3 // IOPRIO_CLASS_IDLE
		classShift = 13
	)
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, whoProcess, 0, classIdle<<classShift)
}
