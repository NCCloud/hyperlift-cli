package update

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// reexecProcess replaces the current process image with path, and passes args
// and the current environment. On Unix it calls execve through syscall.Exec, so
// it does not return on success. Windows has no execve, so there it starts a
// child and forwards the streams. When the child succeeds, the parent exits 0.
// A child error is returned to the caller, which deliberately downgrades it to
// a warning (see update.go): the update already succeeded, so a failed re-exec
// does not change the exit code.
func reexecProcess(path string, args []string) error {
	if runtime.GOOS == "windows" {
		// There is no context here. This re-execs the CLI's own binary as the
		// last act of the update, and must outlive any request-scoped context.
		cmd := exec.Command(path, args[1:]...) //nolint:gosec,noctx // path is our own binary; intentionally context-free
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		cmd.Env = os.Environ()
		if err := cmd.Run(); err != nil {
			return err
		}

		os.Exit(0)

		return nil
	}

	return syscall.Exec(path, args, os.Environ()) //nolint:gosec // path is our own binary
}
