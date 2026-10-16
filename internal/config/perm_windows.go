//go:build windows

package config

import "os"

// checkSecretPerm: permissao e dono nao se aplicam no Windows (so desenvolvimento).
func checkSecretPerm(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return nil
}

// readIssabelKeyFile: so existencia no Windows (desenvolvimento), como checkSecretPerm.
func readIssabelKeyFile(path string) ([]byte, error) { return os.ReadFile(path) }
