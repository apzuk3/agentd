package main

import (
	"context"
	"database/sql"
	"encoding/base64"
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
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	"github.com/syss-io/executor/gen/proto/go/assets/v1/assetsv1connect"
	"github.com/syss-io/executor/gen/proto/go/executor/v1/executorv1connect"
	"github.com/syss-io/executor/gen/proto/go/identity/v1/identityv1connect"
	"github.com/syss-io/executor/gen/proto/go/sessiontoken/v1/sessiontokenv1connect"
	"github.com/syss-io/executor/internal/assets"
	"github.com/syss-io/executor/internal/executor"
	"github.com/syss-io/executor/internal/identity"
	"github.com/syss-io/executor/internal/logger"
	"github.com/syss-io/executor/internal/sessiontoken"
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
					&cli.StringFlag{
						Name:    "paseto-key",
						Usage:   "Base64-encoded 32-byte PASETO symmetric key",
						Sources: cli.EnvVars("PASETO_KEY"),
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

					if err := gormDB.AutoMigrate(
						&domainv1.SessionEventORM{},
						&domainv1.APIKeyORM{},
						&domainv1.SessionTokenORM{},
						&domainv1.WorkspaceORM{},
						&domainv1.FolderORM{},
						&domainv1.InstructionORM{},
					); err != nil {
						return fmt.Errorf("failed to auto migrate: %w", err)
					}

					pasetoKeyB64 := cmd.String("paseto-key")
					var pasetoKey []byte
					if pasetoKeyB64 != "" {
						var err error
						pasetoKey, err = base64.StdEncoding.DecodeString(pasetoKeyB64)
						if err != nil {
							return fmt.Errorf("failed to decode PASETO key: %w", err)
						}
						if len(pasetoKey) != 32 {
							return fmt.Errorf("PASETO key must be 32 bytes, got %d", len(pasetoKey))
						}
					}

					sessionTokenService := &sessiontoken.Service{
						DB:        gormDB,
						PasetoKey: pasetoKey,
					}
					sessionTokenPath, sessionTokenHandler := sessiontokenv1connect.NewSessionTokenServiceHandler(sessionTokenService)

					executorService := &executor.ServiceImpl{
						Logger:         logger.NewSQLiteLogger(gormDB),
						DB:             gormDB,
						TokenValidator: sessionTokenService,
					}
					executorPath, executorHandler := executorv1connect.NewExecutorHandler(executorService)

					identityService := &identity.Service{
						DB: gormDB,
					}
					identityPath, identityHandler := identityv1connect.NewIdentityHandler(identityService)

					assetsService := &assets.Service{
						DB: gormDB,
					}
					assetsPath, assetsHandler := assetsv1connect.NewAssetsHandler(assetsService)

					mux := http.NewServeMux()
					mux.Handle(executorPath, executorHandler)
					mux.Handle(identityPath, identityHandler)
					mux.Handle(assetsPath, assetsHandler)
					mux.Handle(sessionTokenPath, sessionTokenHandler)

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
