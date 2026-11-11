//go:build !unix

package appdir

import "os"

func privateReadFlags() int { return os.O_RDONLY }
