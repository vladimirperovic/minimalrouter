//go:build !linux

package recovery

import "errors"

func openMigrationDBWorker(string) (migrationDB, error) {
	return nil, errors.New("migration database worker requires Linux")
}

func hardenMigrationDBWorker() error {
	return errors.New("migration database worker requires Linux")
}
