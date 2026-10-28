// Command seed migrates the database and loads the synthetic railway world.
// It is idempotent: a database seeded by the current generator version is left
// alone unless SEED_FORCE=1; an older version is replaced.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/config"
	"github.com/yatrax/yatrax/services/core/internal/db"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	cfg := config.Load()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	have, err := seed.SeededVersion(ctx, pool)
	if err != nil {
		return err
	}
	force := os.Getenv("SEED_FORCE") == "1"
	switch {
	case have == seed.Version && !force:
		slog.Info("database already seeded; set SEED_FORCE=1 to reseed", "version", have)
		return nil
	case have != "":
		// Synthetic data only: reseeding replaces every account, including sign-ups.
		slog.Warn("reseeding: existing data will be replaced", "had_version", have, "version", seed.Version, "forced", force)
		if err := seed.Reset(ctx, pool); err != nil {
			return err
		}
	}

	randomSeed := uint64(config.Int("SEED_RANDOM", 42))
	passengers := config.Int("SEED_PASSENGERS", 50000)
	start := time.Now()
	world, err := seed.Generate(randomSeed, passengers)
	if err != nil {
		return err
	}
	slog.Info("generated world", "stations", len(world.Stations), "trains", len(world.Trains),
		"people", len(world.People), "vendors", len(world.Vendors), "took", time.Since(start).String())
	if err := seed.Write(ctx, pool, world, randomSeed, auth.DemoPassword); err != nil {
		return err
	}
	slog.Info("seed complete", "took", time.Since(start).String())
	return nil
}
