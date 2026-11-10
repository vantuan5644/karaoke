package voice

import (
	"os"
	"syscall"
	"unsafe"
)

// childAttr ends the PipeWire tools with the server, even if the server is killed.
func childAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM} }

// queuedBytes is how much has been written to a pipe and not read yet.
func queuedBytes(f *os.File) int {
	raw, err := f.SyscallConn()
	if err != nil {
		return 0
	}
	n := int32(0)
	raw.Control(func(fd uintptr) {
		syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCINQ, uintptr(unsafe.Pointer(&n)))
	})
	return int(n)
}
