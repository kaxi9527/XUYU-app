//go:build unix

package appdir

import (
	"os"
	"syscall"
)

func privateReadFlags() int { return os.O_RDONLY | syscall.O_NONBLOCK }
