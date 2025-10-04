package main

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestParseNHECSV(t *testing.T) {
	data, err := parse("NHE2023.csv")
	assert.NoError(t, err)
	assert.NotNil(t, data)

	assert.Equal(t, 64, len(data.Years))
	assert.Equal(t, 1960, data.Years[0])
	assert.Equal(t, 2023, data.Years[63])

	assert.True(t, len(data.Categories) > 0)

	firstCat := data.Categories[0]
	assert.Equal(
		t,
		"Total National Health Expenditures",
		firstCat.Name,
	)
	assert.Equal(t, 0, firstCat.ParentID)
	assert.Equal(t, 0, firstCat.IndentLevel)
	assert.True(t, firstCat.IsMajorHeading)

	foundOutOfPocket := false
	for _, cat := range data.Categories {
		if cat.Name == "Out of pocket" {
			foundOutOfPocket = true
			assert.Equal(t, 5, cat.IndentLevel)
			assert.False(t, cat.IsMajorHeading)
			break
		}
	}
	assert.True(t, foundOutOfPocket)

	assert.Equal(t, len(data.Categories), len(data.Expenditures))

	for catID, yearMap := range data.Expenditures {
		assert.True(t, catID > 0)
		assert.True(t, len(yearMap) > 0)
	}

	firstCatExpend := data.Expenditures[1]
	val1960 := firstCatExpend[1]
	assert.NotNil(t, val1960)
	assert.Equal(t, 27122, *val1960)

	foundMedicare := false
	for idx, cat := range data.Categories {
		if cat.Name == "Medicare" {
			foundMedicare = true
			catID := idx + 1
			expend := data.Expenditures[catID]
			val1960 := expend[1]
			assert.Nil(t, val1960)
			break
		}
	}
	assert.True(t, foundMedicare)
}

func TestLoadParsedData(t *testing.T) {
	data, err := parse("NHE2023.csv")
	assert.NoError(t, err)

	dbName := os.Getenv("TEST_DB")
	if dbName == "" {
		dbName = ":memory:"
	}

	db, err := sql.Open("sqlite3", dbName)
	assert.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(schemaSQL)
	assert.NoError(t, err)

	err = loadParsed(db, data)
	assert.NoError(t, err)

	var yearCount int
	err = db.QueryRow("SELECT COUNT(*) FROM years").Scan(&yearCount)
	assert.NoError(t, err)
	assert.Equal(t, 64, yearCount)

	var categoryCount int
	err = db.QueryRow("SELECT COUNT(*) FROM categories").Scan(&categoryCount)
	assert.NoError(t, err)
	assert.Equal(t, len(data.Categories), categoryCount)

	var expenditureCount int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM expenditures",
	).Scan(&expenditureCount)
	assert.NoError(t, err)
	assert.True(t, expenditureCount > 0)

	var year int
	err = db.QueryRow(
		"SELECT year FROM years ORDER BY year LIMIT 1",
	).Scan(&year)
	assert.NoError(t, err)
	assert.Equal(t, 1960, year)

	var catName string
	err = db.QueryRow(
		"SELECT name FROM categories ORDER BY sort_order LIMIT 1",
	).Scan(&catName)
	assert.NoError(t, err)
	assert.Equal(t, "Total National Health Expenditures", catName)

	var amount int
	err = db.QueryRow(
		`SELECT e.amount
		FROM expenditures e
		JOIN categories c ON c.id = e.category_id
		JOIN years y ON y.id = e.year_id
		WHERE c.name = 'Total National Health Expenditures'
		AND y.year = 1960`,
	).Scan(&amount)
	assert.NoError(t, err)
	assert.Equal(t, 27122, amount)

	var nullCount int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM expenditures WHERE amount IS NULL",
	).Scan(&nullCount)
	assert.NoError(t, err)
	assert.True(t, nullCount > 0)
}
