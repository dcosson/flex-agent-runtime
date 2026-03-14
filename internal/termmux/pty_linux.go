//go:build linux

package termmux

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// openPTY opens a new PTY master/slave pair.
// On Linux, uses TIOCGPTN to get the slave device number.
func openPTY() (master, slave *os.File, err error) {
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	if err := unlockpt(ptm); err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("unlockpt: %w", err)
	}

	name, err := ptsname(ptm)
	if err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("ptsname: %w", err)
	}

	pts, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("open pts: %w", err)
	}

	return ptm, pts, nil
}

// ptsname returns the name of the slave PTY.
// On Linux, uses TIOCGPTN ioctl to get the device number.
func ptsname(f *os.File) (string, error) {
	var n uint32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	if errno != 0 {
		return "", errno
	}
	return "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}

func unlockpt(f *os.File) error {
	var unlock int32 // 0 = unlock
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))
	if errno != 0 {
		return errno
	}
	return nil
}

// setPTYSize sets the window size of the PTY.
func setPTYSize(f *os.File, rows, cols int) error {
	ws := struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{
		Row: uint16(rows),
		Col: uint16(cols),
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return errno
	}
	return nil
}
