package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // bare-metal without zoneinfo: LoadLocation still works (Docker already ships tzdata)

	"wiminder/internal/api"
	"wiminder/internal/calendarprov"
	"wiminder/internal/config"
	"wiminder/internal/notify"
	"wiminder/internal/scheduler"
	"wiminder/internal/secret"
	"wiminder/internal/store"
)

func main() {
	config.LoadDotEnv(".env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("failed to create data dir", "dir", cfg.DataDir, "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		slog.Error("failed to open db", "path", cfg.DBPath(), "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	key := secret.DeriveKey(cfg.AppSecret)
	if _, err := api.SeedDevTelegram(context.Background(), st, key, cfg); err != nil {
		slog.Error("dev telegram seed", "err", err)
	}
	// api-harilibur mirrors: pages.dev primary, netlify.app fallback. The old
	// sources are dead — dayoffapi.vercel.app (402 DEPLOYMENT_DISABLED) and
	// artworks.kresna.me (404). One source, two categories via is_national_holiday.
	hariliburPrimary := "https://api-harilibur.pages.dev"
	hariliburFallback := "https://api-harilibur.netlify.app"
	providers := []calendarprov.Provider{
		calendarprov.NewComputedPawukon(),
		calendarprov.NewCachedRemote(calendarprov.NewKresnaFiltered(hariliburPrimary, hariliburFallback, true), st),
		calendarprov.NewCachedRemote(calendarprov.NewKresnaFiltered(hariliburPrimary, hariliburFallback, false), st),
	}
	srv := api.NewServer(cfg, st, providers)

	svc := &scheduler.Service{
		St:    st,
		Clock: scheduler.RealClock{},
		Resolve: func(ctx context.Context, ch store.Channel) (notify.Notifier, error) {
			return notify.NewFromChannel(ch, key)
		},
		Providers: providers,
	}
	// The same snapshot builder is used by the runner adapter and the Loop — DRY.
	buildSnapshot := func(ctx context.Context) scheduler.Snapshot {
		set := srv.LoadSettings(ctx)
		return scheduler.Snapshot{
			Timezone: set.Timezone, SendTime: set.SendTime, CatchUpHours: set.CatchUpHours,
			DefaultOffsets: set.DefaultOffsets, DefaultChannelIDs: set.DefaultChannelIDs,
			HolidayCategories: set.HolidayCategories,
			HolidayOffsets:    set.HolidayOffsets,
			RecurrenceOffsets: set.RecurrenceOffsets,
		}
	}
	srv.SetRunner(api.SchedulerRunnerFunc(func(ctx context.Context) (api.RunResult, error) {
		res, err := svc.RunOnce(ctx, buildSnapshot(ctx))
		return api.RunResult(res), err
	}))

	ctxLoop, cancelLoop := context.WithCancel(context.Background())
	defer cancelLoop()
	go svc.Loop(ctxLoop, time.Minute, func(ctx context.Context) (scheduler.Snapshot, error) {
		return buildSnapshot(ctx), nil
	})

	httpServer := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("wiminder running", "addr", cfg.Addr, "auth", cfg.AuthMode)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutdown...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}
