// migrate.go runs SQL migrations using golang-migrate.
// It owns the migration runner, up and down by one step.
// It must never modify migration files.
// It relies on migrations stored in db/migrations/ as numbered .sql files.
package platform

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations applies or rolls back database migrations.
// direction must be "up" or "down".
// Returns the number of migrations applied (approximate) or an error.
func RunMigrations(databaseURL, direction string) (int, error) {
	m, err := migrate.New("file://db/migrations", databaseURL)
	if err != nil {
		return 0, fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	var migErr error
	switch direction {
	case "up":
		migErr = m.Up()
	case "down":
		migErr = m.Steps(-1)
	default:
		return 0, fmt.Errorf("unknown direction %q: use 'up' or 'down'", direction)
	}

	if migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		return 0, migErr
	}

	ver, _, _ := m.Version()
	return int(ver), nil
}
