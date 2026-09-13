// Package migratecmd exposes schema management as an explicit command.
//
// Migrations normally run automatically at startup, which is what makes `dev.ps1` a one
// liner. But operations needs the two other things: apply the schema without starting the
// server (so a release can be prepared before the rollout), and check whether the running
// database is at the expected version (so a deployment can fail fast instead of serving
// errors).
package migratecmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/migrate"
)

// NewCommand builds the `migrate` command.
func NewCommand(log *zap.Logger) *cli.Command {
	var checkOnly bool

	return &cli.Command{
		Name:  "migrate",
		Usage: "Apply or check the database schema (migrations also run on `start`)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "database-url",
				Usage:    "PostgreSQL DSN",
				Required: true,
				Sources:  cli.EnvVars("DATABASE_URL"),
			},
			&cli.BoolFlag{
				Name:    "check",
				Usage:   "only verify that every migration has been applied (exit 1 if not)",
				Sources: cli.EnvVars("MIGRATE_CHECK"),
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			checkOnly = c.Bool("check")

			pool, err := pgxpool.New(ctx, c.String("database-url"))
			if err != nil {
				return fmt.Errorf("cannot parse database url: %w", err)
			}

			defer pool.Close()

			if err = pool.Ping(ctx); err != nil {
				return fmt.Errorf("cannot reach the database: %w", err)
			}

			if checkOnly {
				if err = migrate.Check(ctx, pool); err != nil {
					if errors.Is(err, migrate.ErrNotInitialized) {
						log.Error("schema is not initialized", zap.Error(err))
					}

					return err
				}

				log.Info("schema is up to date")

				return nil
			}

			applied, err := migrate.Up(ctx, pool)
			if err != nil {
				return err
			}

			if len(applied) == 0 {
				log.Info("schema is up to date, nothing to apply")

				return nil
			}

			log.Info("migrations applied", zap.Strings("versions", applied))

			return nil
		},
	}
}
