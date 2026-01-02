package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	"github.com/urfave/cli/v3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/syss-io/executor/client"
	"github.com/syss-io/executor/gen/proto/go/agent/v1/agentv1connect"
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	"github.com/syss-io/executor/internal/agent"
	"github.com/syss-io/executor/internal/logger"
)

func main() {
	cmd := &cli.Command{
		Name:  "executor",
		Usage: "Executor CLI",
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "Start the agent service",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "port",
						Value:   "8080",
						Usage:   "Port to listen on",
						Sources: cli.EnvVars("PORT"),
					},
					&cli.StringFlag{
						Name:    "turso-db-url",
						Usage:   "Turso database URL",
						Value:   "file:./executor.db",
						Sources: cli.EnvVars("TURSO_CONNECTION_PATH"),
					},
					&cli.StringFlag{
						Name:    "turso-db-token",
						Usage:   "Turso database auth token",
						Sources: cli.EnvVars("TURSO_CONNECTION_TOKEN"),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					err := godotenv.Load()
					if err != nil {
						log.Println("Warning: Error loading .env file")
					}

					port := cmd.String("port")
					url := cmd.String("turso-db-url")
					token := cmd.String("turso-db-token")

					connStr := url
					if token != "" {
						connStr = fmt.Sprintf("%s?authToken=%s", url, token)
					}

					slog.Info("Starting agent service", "port", port)

					sqlDB, err := sql.Open("libsql", connStr)
					if err != nil {
						return fmt.Errorf("failed to open sql connection: %w", err)
					}

					gormDB, err := gorm.Open(sqlite.Dialector{Conn: sqlDB}, &gorm.Config{})
					if err != nil {
						return fmt.Errorf("failed to open gorm connection: %w", err)
					}

					if err := gormDB.AutoMigrate(&domainv1.SessionLogORM{}); err != nil {
						return fmt.Errorf("failed to auto migrate: %w", err)
					}

					service := &agent.ServiceImpl{
						Logger: logger.NewSQLiteLogger(gormDB),
						DB:     gormDB,
					}
					path, handler := agentv1connect.NewExecutorHandler(service)

					mux := http.NewServeMux()
					mux.Handle(path, handler)

					server := &http.Server{
						Addr:    ":" + port,
						Handler: h2c.NewHandler(mux, &http2.Server{}),
					}

					return server.ListenAndServe()
				},
			},
			{
				Name:  "client",
				Usage: "Run the agent client",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "url",
						Value:   "http://localhost:8080",
						Usage:   "Server URL",
						Sources: cli.EnvVars("SERVER_URL"),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					url := cmd.String("url")
					slog.Info("Starting agent client", "url", url)

					// We'll implement this in internal/client
					return client.RunClient(ctx, url)
				},
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
