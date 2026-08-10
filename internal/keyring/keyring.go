// Package keyring stores the account API secret in the OS keyring, or in a 0600
// file when no keyring backend exists. On a read, an environment variable can
// override the stored value.
package keyring

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	zk "github.com/zalando/go-keyring"

	"github.com/nccloud/hyperlift-cli/internal/config"
)

// Service is the keyring service name that holds the secret.
const Service = "hyperlift-cli"

// account is the fixed keyring entry key of the single-account secret.
const account = "default"

// EnvAPISecret overrides the stored secret on a read.
const EnvAPISecret = "HYPERLIFT_API_SECRET" //nolint:gosec // env-var name, not a credential

// FallbackPath returns the file that stores the secret when the OS keyring is
// not available.
func FallbackPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "secrets", account+".secret"), nil
}

// SetSecret stores the account API secret. It prefers the OS keyring, and falls
// back to a 0600 file when no keyring backend exists. usedFallback reports the
// fallback, so the caller can warn the user.
func SetSecret(secret string) (usedFallback bool, err error) {
	if err := zk.Set(Service, account, secret); err == nil {
		// Remove any fallback file from an earlier keyring outage: a stale
		// file must not linger as plaintext or shadow material.
		if err := removeFallbackFile(); err != nil {
			return false, err
		}

		return false, nil
	}

	// The keyring is not available, so write a 0600 file instead. Delete any old
	// keyring entry first: GetSecret reads the keyring before the file, so a stale
	// entry would shadow the fresher file forever. The delete is best effort: the
	// same backend just failed the write.
	_ = zk.Delete(Service, account)

	p, err := FallbackPath()
	if err != nil {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return false, fmt.Errorf("create secrets dir: %w", err)
	}

	// os.WriteFile keeps the mode of an existing file, so write a fresh 0600
	// temp file and rename it over the target.
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".tmp-")
	if err != nil {
		return false, fmt.Errorf("write secret fallback: %w", err)
	}

	if _, err := tmp.Write([]byte(secret)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return false, fmt.Errorf("write secret fallback: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return false, fmt.Errorf("write secret fallback: %w", err)
	}

	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return false, fmt.Errorf("replace secret fallback: %w", err)
	}

	return true, nil
}

// GetSecret resolves the account API secret. The HYPERLIFT_API_SECRET
// environment variable always wins. Without it, GetSecret reads the OS keyring,
// then the fallback file. It returns ("", nil) when nothing is stored.
func GetSecret() (string, error) {
	if v := os.Getenv(EnvAPISecret); v != "" {
		return v, nil
	}

	// A missing entry and an unavailable backend both fall through to the file.
	if secret, err := zk.Get(Service, account); err == nil {
		return secret, nil
	}

	p, ferr := FallbackPath()
	if ferr != nil {
		return "", ferr
	}

	data, ferr := os.ReadFile(p) //nolint:gosec // p is the CLI's own secret-fallback path under the config dir
	if ferr != nil {
		if errors.Is(ferr, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("read secret fallback: %w", ferr)
	}

	return string(data), nil
}

// DeleteSecret removes the stored secret from the keyring and from the fallback
// file. A missing entry is not an error.
func DeleteSecret() error {
	// Ignore every keyring backend error, including "not found". The cleanup
	// below still runs.
	_ = zk.Delete(Service, account)
	return removeFallbackFile()
}

// removeFallbackFile deletes the fallback file. A missing file is not an error.
func removeFallbackFile() error {
	p, err := FallbackPath()
	if err != nil {
		return err
	}

	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove secret fallback: %w", err)
	}

	return nil
}
