package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/cli/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

//go:embed schema.sql
var schemaSQL string

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/css/output.css
var staticFS embed.FS

var csvFilename = "NHE2023.csv"

type App struct {
	db     *sql.DB
	server *http.Server
}

type Category struct {
	Name           string
	ParentID       int
	IndentLevel    int
	SortOrder      int
	IsMajorHeading bool
}

type ParsedData struct {
	Years        []int
	Categories   []Category
	Expenditures map[int]map[int]*int
}

type TableData struct {
	Years      []int
	Categories []TableCategory
	Totals     map[int]*int
}

type TableCategory struct {
	Name   string
	Values []*int
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

	if fn := os.Getenv("NHE_CSV"); fn != "" {
		csvFilename = fn
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
		app    = &App{}
		dbPath string
	)

	cliApp := &cli.App{
		Name:  "nhe",
		Usage: "NHE data server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "db",
				Value:       "app.db",
				Usage:       "path to SQLite database file",
				Destination: &dbPath,
			},
			&cli.BoolFlag{
				Name:  "force-load",
				Usage: "force reload data from CSV",
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

			app.db = db

			forceLoad := c.Bool("force-load")
			if forceLoad {
				if err := clearDatabase(db); err != nil {
					return fmt.Errorf("clear database: %w", err)
				}
			}

			needsLoad, err := databaseEmpty(db)
			if err != nil {
				return fmt.Errorf("check database: %w", err)
			}

			if needsLoad || forceLoad {
				slog.Info("loading data from CSV", "file", csvFilename)
				data, err := parse(csvFilename)
				if err != nil {
					return fmt.Errorf("parse CSV: %w", err)
				}

				if err := loadParsed(db, data); err != nil {
					return fmt.Errorf("load data: %w", err)
				}

				slog.Info(
					"data loaded",
					"categories",
					len(data.Categories),
					"years",
					len(data.Years),
				)
			}

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
			{
				Name:      "dump",
				Usage:     "dump database contents as text table",
				ArgsUsage: "[year]",
				Action: func(c *cli.Context) error {
					return dumpCmd(app, c)
				},
			},
			{
				Name:  "load",
				Usage: "load data from CSV into database",
				Action: func(c *cli.Context) error {
					if err := clearDatabase(app.db); err != nil {
						return fmt.Errorf("clear database: %w", err)
					}

					slog.Info("loading data from CSV", "file", csvFilename)
					data, err := parse(csvFilename)
					if err != nil {
						return fmt.Errorf("parse CSV: %w", err)
					}

					if err := loadParsed(app.db, data); err != nil {
						return fmt.Errorf("load data: %w", err)
					}

					slog.Info(
						"data loaded",
						"categories",
						len(data.Categories),
						"years",
						len(data.Years),
					)
					return nil
				},
			},
		},
	}

	if err := cliApp.Run(os.Args); err != nil {
		fatal("app failed", "error", err)
	}
}

func parse(filename string) (*ParsedData, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 3 {
		return nil, fmt.Errorf("CSV too short")
	}

	yearRow := records[1]
	years := make([]int, 0, len(yearRow)-1)
	for i := 1; i < len(yearRow); i++ {
		year, err := strconv.Atoi(yearRow[i])
		if err != nil {
			return nil, fmt.Errorf("invalid year at column %d: %v", i, err)
		}
		years = append(years, year)
	}

	data := &ParsedData{
		Years:        years,
		Categories:   make([]Category, 0),
		Expenditures: make(map[int]map[int]*int),
	}

	var (
		parentStack = []int{}
		last        = -1
		categoryID  = 0
	)

	for rowIdx := 2; rowIdx < len(records); rowIdx++ {
		row := records[rowIdx]
		if len(row) == 0 || row[0] == "" {
			continue
		}

		var (
			label  = row[0]
			indent = ldSpc(label)
			name   = strings.TrimSpace(label)
		)

		if name == "" {
			continue
		}

		categoryID++
		parentID := 0

		if indent > last {
			if categoryID > 1 {
				parentID = categoryID - 1
				parentStack = append(parentStack, parentID)
			}
		} else if indent < last {
			for len(parentStack) > 0 && indent <= last {
				parentStack = parentStack[:len(parentStack)-1]
				last -= 5
			}
			if len(parentStack) > 0 {
				parentID = parentStack[len(parentStack)-1]
			}
		} else {
			if len(parentStack) > 0 {
				parentID = parentStack[len(parentStack)-1]
			}
		}

		isMajorHeading := indent == 0 &&
			name != "POPULATION" &&
			!strings.HasPrefix(name, "Total CMS Programs")

		cat := Category{
			Name:           name,
			ParentID:       parentID,
			IndentLevel:    indent,
			SortOrder:      rowIdx - 1,
			IsMajorHeading: isMajorHeading,
		}
		data.Categories = append(data.Categories, cat)

		data.Expenditures[categoryID] = make(map[int]*int)
		for i := 1; i < len(row) && i <= len(years); i++ {
			val := strings.TrimSpace(row[i])
			if val == "" || val == "-" {
				data.Expenditures[categoryID][i] = nil
				continue
			}

			val = strings.ReplaceAll(val, ",", "")
			val = strings.Trim(val, "\"")

			// simple static data set
			amount, _ := strconv.Atoi(val)

			data.Expenditures[categoryID][i] = &amount
		}

		last = indent
	}

	return data, nil
}

func ldSpc(s string) int {
	count := 0
	for _, ch := range s {
		if ch == ' ' {
			count++
		} else {
			break
		}
	}
	return count
}

func loadParsed(db *sql.DB, data *ParsedData) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, year := range data.Years {
		_, err := tx.Exec(
			"INSERT OR IGNORE INTO years (year) VALUES (?)",
			year,
		)
		if err != nil {
			return fmt.Errorf("insert year %d: %w", year, err)
		}
	}

	yearIDMap := make(map[int]int)
	rows, err := tx.Query("SELECT id, year FROM years")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, year int
		if err := rows.Scan(&id, &year); err != nil {
			rows.Close()
			return err
		}
		yearIDMap[year] = id
	}
	rows.Close()

	categoryIDMap := make(map[int]int)

	for idx, cat := range data.Categories {
		categoryNum := idx + 1

		var parentID *int
		if cat.ParentID > 0 {
			if dbParentID, ok := categoryIDMap[cat.ParentID]; ok {
				parentID = &dbParentID
			}
		}

		var isMajorHeading int
		if cat.IsMajorHeading {
			isMajorHeading = 1
		}

		result, err := tx.Exec(
			`INSERT INTO categories
			(name, parent_id, indent_level, sort_order, is_major_heading)
			VALUES (?, ?, ?, ?, ?)`,
			cat.Name,
			parentID,
			cat.IndentLevel,
			cat.SortOrder,
			isMajorHeading,
		)
		if err != nil {
			return fmt.Errorf("insert category %s: %w", cat.Name, err)
		}

		lastID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		categoryIDMap[categoryNum] = int(lastID)
	}

	for catNum, yearMap := range data.Expenditures {
		dbCategoryID, ok := categoryIDMap[catNum]
		if !ok {
			continue
		}

		for yearIdx, amount := range yearMap {
			if yearIdx < 1 || yearIdx > len(data.Years) {
				continue
			}

			year := data.Years[yearIdx-1]
			yearID, ok := yearIDMap[year]
			if !ok {
				continue
			}

			_, err := tx.Exec(
				`INSERT INTO expenditures
				(category_id, year_id, amount)
				VALUES (?, ?, ?)`,
				dbCategoryID,
				yearID,
				amount,
			)
			if err != nil {
				return fmt.Errorf(
					"insert expenditure cat=%d year=%d: %w",
					dbCategoryID,
					yearID,
					err,
				)
			}
		}
	}

	return tx.Commit()
}

func databaseEmpty(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM categories").Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func clearDatabase(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM expenditures"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM categories"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM years"); err != nil {
		return err
	}

	return tx.Commit()
}

// this is really just sanity check code
func dumpCmd(app *App, c *cli.Context) error {
	year := 2023
	if c.Args().Len() > 0 {
		y, err := strconv.Atoi(c.Args().First())
		if err != nil {
			return fmt.Errorf("invalid year: %v", err)
		}
		year = y
	}

	rows, err := app.db.Query(`
		SELECT
			c.name,
			c.indent_level,
			e.amount
		FROM expenditures e
		JOIN categories c ON c.id = e.category_id
		JOIN years y ON y.id = e.year_id
		WHERE y.year = ?
		ORDER BY c.sort_order
	`, year)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Printf("National Health Expenditures - Year %d\n", year)
	fmt.Printf("%s\n", strings.Repeat("=", 70))
	fmt.Printf("%-60s  %10s\n", "CATEGORY", "AMOUNT")
	fmt.Printf("%s\n", strings.Repeat("-", 70))

	for rows.Next() {
		var (
			name   string
			indent int
			amount *int
		)

		if err := rows.Scan(&name, &indent, &amount); err != nil {
			return err
		}

		var (
			indentStr = strings.Repeat("  ", indent/5)
			fullName  = indentStr + name
		)

		amountStr := "N/A"
		if amount != nil {
			amountStr = fmt.Sprintf("%d", *amount)
		}

		fmt.Printf("%-60s  %10s\n", fullName, amountStr)
	}

	return rows.Err()
}

func nheData(db *sql.DB) (*TableData, error) {
	allYears := []int{}

	rows, err := db.Query("SELECT year FROM years ORDER BY year")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var year int
		if err := rows.Scan(&year); err != nil {
			rows.Close()
			return nil, err
		}
		allYears = append(allYears, year)
	}
	rows.Close()

	// we only display every 3rd year
	displayYears := []int{}
	for i := len(allYears) - 1; i >= 0; i -= 3 {
		displayYears = append(displayYears, allYears[i])
	}

	totals := map[int]*int{}
	for _, year := range displayYears {
		var total *int
		err := db.QueryRow(`
			SELECT e.amount
			FROM expenditures e
			JOIN years y ON y.id = e.year_id
			JOIN categories c ON c.id = e.category_id
			WHERE y.year = ? AND c.name = 'Total National Health Expenditures'
		`, year).Scan(&total)
		if err == nil {
			totals[year] = total
		}
	}

	rows, err = db.Query(`
		SELECT id, name
		FROM categories
		WHERE is_major_heading = 1
		ORDER BY sort_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []TableCategory
	for rows.Next() {
		var (
			id   int
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}

		values := make([]*int, len(displayYears))
		hasData := false
		for i, year := range displayYears {
			var amount *int
			err := db.QueryRow(`
				SELECT e.amount
				FROM expenditures e
				JOIN years y ON y.id = e.year_id
				WHERE e.category_id = ? AND y.year = ?
			`, id, year).Scan(&amount)
			if err == nil {
				values[i] = amount
				if amount != nil {
					hasData = true
				}
			}
		}

		if hasData {
			categories = append(categories, TableCategory{
				Name:   name,
				Values: values,
			})
		}
	}

	return &TableData{
		Years:      displayYears,
		Categories: categories,
		Totals:     totals,
	}, nil
}

func serveCmd(app *App, c *cli.Context) error {
	mux := http.NewServeMux()

	funcMap := template.FuncMap{
		"formatNumber": func(n *int) string {
			if n == nil {
				return "N/A"
			}
			val := float64(*n)
			if val >= 1000000 {
				return fmt.Sprintf("$%.2fT", val/1000000)
			} else if val >= 1000 {
				return fmt.Sprintf("$%.2fB", val/1000)
			}
			return fmt.Sprintf("$%.2fM", val)
		},
		"formatPercent": func(amount *int, year int, totals map[int]*int) string {
			if amount == nil {
				return ""
			}
			total, ok := totals[year]
			if !ok || total == nil || *total == 0 {
				return ""
			}
			pct := float64(*amount) / float64(*total) * 100
			return fmt.Sprintf("%.1f%%", pct)
		},
		"trimPrefix": func(s, prefix string) string {
			return strings.TrimPrefix(s, prefix)
		},
		"heatmapColor": func(amount *int, year int, totals map[int]*int, catIdx int) string {
			if catIdx < 3 {
				return "bg-gray-100"
			}
			if amount == nil {
				return "bg-gray-100"
			}
			total, ok := totals[year]
			if !ok || total == nil || *total == 0 {
				return "bg-gray-100"
			}
			pct := float64(*amount) / float64(*total) * 100

			if pct >= 15 {
				return "bg-red-200"
			} else if pct >= 13.5 {
				return "bg-orange-200"
			} else if pct >= 12 {
				return "bg-amber-200"
			} else if pct >= 10.5 {
				return "bg-yellow-200"
			} else if pct >= 9 {
				return "bg-lime-200"
			} else if pct >= 7.5 {
				return "bg-green-200"
			} else if pct >= 6 {
				return "bg-teal-200"
			} else if pct >= 4.5 {
				return "bg-cyan-200"
			} else if pct >= 3 {
				return "bg-sky-200"
			} else if pct >= 1.5 {
				return "bg-blue-200"
			} else {
				return "bg-blue-200"
			}
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(
		templateFS,
		"templates/*.html",
	)
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("sub static: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := nheData(app.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	app.server = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	slog.Info("starting server", "addr", app.server.Addr)
	return app.server.ListenAndServe()
}
