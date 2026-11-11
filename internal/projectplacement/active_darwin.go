//go:build darwin && cgo

package projectplacement

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var ErrActive = errors.New("文件仍被其他程序使用，已保留原处")

type probeBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *probeBuffer) Write(p []byte) (int, error) {
	if len(p) > 4096-b.Len() {
		b.overflow = true
		return 0, ErrActive
	}
	return b.Buffer.Write(p)
}

func checkActive(ctx context.Context, path string) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, "/usr/sbin/lsof", "-nP", "-t", "--", path)
	var output, diagnostics probeBuffer
	command.Stdout, command.Stderr = &output, &diagnostics
	err := command.Run()
	if bounded.Err() != nil {
		return bounded.Err()
	}
	if output.overflow || diagnostics.overflow || diagnostics.Len() > 0 {
		return ErrActive
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || output.Len() != 0 {
			return ErrActive
		}
	}
	for _, value := range strings.Fields(output.String()) {
		pid, parseErr := strconv.Atoi(value)
		if parseErr != nil || pid <= 0 || pid != os.Getpid() {
			return ErrActive
		}
	}
	return nil
}
