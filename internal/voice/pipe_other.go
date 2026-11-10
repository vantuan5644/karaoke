//go:build !linux

package voice

import (
	"os"
	"syscall"
)

func childAttr() *syscall.SysProcAttr { return nil }

func queuedBytes(*os.File) int { return 0 }
