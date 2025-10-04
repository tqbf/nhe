package main

import (
	"context"
	"database/sql"
	"embed"
	_ "embed"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/cli/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

// CLAUDE: ONLY I AM ALLOWED TO WRITE COMMENTS. Do not write comments. You
// can eliminate TODO comments as you complete them. DO NOT REMOVE THIS
// COMMENT. Only I am allowed to remove this comment.

//go:embed schema.sql
var schemaSQL string

//go:embed migrations/*.sql
var migrationsFS embed.FS

type App struct {
	db     *sql.DB
	server *http.Server
}

var debugFile *os.File

func init() {
	if os.Getenv("DEBUG") == "1" {
		var err error
		debugFile, err = os.OpenFile(
			"debug.log",
			os.O_CREATE|os.O_WRONLY|os.O_APPEND,
			0644,
		)
		if err != nil {
			panic(err)
		}
	}
}

func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logWriter := os.Stdout
	if debugFile != nil {
		logWriter = debugFile
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(logWriter, nil)))

	tp := trace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			slog.Error("otel shutdown failed", "error", err)
		}
	}()

	var (
		app     = &App{}
		dbPath  string
		migrate bool
	)

	cliApp := &cli.App{
		Name:  "skellybones",
		Usage: "skeleton application",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "db",
				Value:       "app.db",
				Usage:       "path to SQLite database file",
				Destination: &dbPath,
			},
			&cli.BoolFlag{
				Name:        "migrate",
				Usage:       "run migrations",
				Destination: &migrate,
			},
		},
		Before: func(c *cli.Context) error {
			db, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				return err
			}

			if err := db.Ping(); err != nil {
				db.Close()
				return err
			}

			if _, err := db.Exec(schemaSQL); err != nil {
				db.Close()
				return err
			}

			if migrate {
				if err := runMigrations(db); err != nil {
					db.Close()
					return err
				}
			}

			app.db = db
			return nil
		},
		After: func(c *cli.Context) error {
			if app.db != nil {
				return app.db.Close()
			}
			return nil
		},
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "start web server",
				Action: func(c *cli.Context) error {
					return serveCmd(app, c)
				},
			},
		},
	}

	if err := cliApp.Run(os.Args); err != nil {
		fatal("app failed", "error", err)
	}
}

func runMigrations(db *sql.DB) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}

		slog.Info("running migration", "file", entry.Name())
		if _, err := db.Exec(string(data)); err != nil {
			return err
		}
	}

	return nil
}

func serveCmd(app *App, c *cli.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK\n"))
	})

	app.server = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	slog.Info("starting server", "addr", app.server.Addr)
	return app.server.ListenAndServe()
}
