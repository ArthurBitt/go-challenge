package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.uber.org/fx"

	"junglego/internal/auth"
	"junglego/internal/config"
	"junglego/internal/httpapi"
	"junglego/internal/postgres"
	"junglego/internal/queue"
	"junglego/internal/service"
)

func main() {
	app := fx.New(
		fx.Provide(
			func() (*slog.Logger, error) {
				return slog.New(slog.NewJSONHandler(os.Stdout, nil)), nil
			},
			config.Load,
			func(lc fx.Lifecycle, cfg config.Config) (*postgres.Store, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				store, err := postgres.Connect(ctx, cfg.DatabaseURL)
				if err != nil {
					return nil, err
				}
				lc.Append(fx.Hook{OnStop: func(context.Context) error {
					store.Close()
					return nil
				}})
				return store, nil
			},
			func(store *postgres.Store, log *slog.Logger, cfg config.Config) *service.Service {
				s := service.New(store, log)
				s.ReferenceMaxAttempts = cfg.ReferenceMaxAttempts
				return s
			},
			func(cfg config.Config) (*auth.Verifier, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				return auth.New(ctx, cfg.OIDCIssuerURL, cfg.OIDCAudience)
			},
			func(cfg config.Config) (*queue.Client, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return queue.NewClient(ctx, cfg)
			},
			func(log *slog.Logger, svc *service.Service, store *postgres.Store, client *queue.Client, cfg config.Config) *queue.Workers {
				allowed := map[string]struct{}{}
				for _, p := range cfg.AllowedProviders {
					allowed[p] = struct{}{}
				}
				return &queue.Workers{Log: log, Svc: svc, Store: store, Client: client, AllowedProviders: allowed}
			},
			func(svc *service.Service, log *slog.Logger, store *postgres.Store, q *queue.Client) *httpapi.API {
				return &httpapi.API{
					Svc: svc,
					Log: log,
					Ready: func(r *http.Request) error {
						if err := store.Ping(r.Context()); err != nil {
							return err
						}
						return q.Ping(r.Context())
					},
				}
			},
		),
		fx.Invoke(run),
	)
	app.Run()
}

func run(lc fx.Lifecycle, cfg config.Config, api *httpapi.API, verifier *auth.Verifier, workers *queue.Workers) {
	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           httpapi.NewRouter(api, verifier),
		ReadHeaderTimeout: 5 * time.Second,
	}
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go workers.RunConsumer(workerCtx)
			go workers.RunPublisher(workerCtx)
			go workers.RunReferences(workerCtx)
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "http: %v\n", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			cancelWorkers()
			return srv.Shutdown(ctx)
		},
	})
}
