package main

import (
	"database/sql"
	"encoding/csv"
	"os"
	"strconv"
)

type ExportField string

const (
	FieldCardID      ExportField = "card_id"
	FieldNormalCount ExportField = "normal_count"
	FieldFoilCount   ExportField = "foil_count"
	FieldTotalCount  ExportField = "total_count"
)

type ExportPreset struct {
	Name         string        `json:"name"`
	Fields       []ExportField `json:"fields"`
	CardIDTrimEnd int          `json:"card_id_trim_end"` // 0 = full ID, N = remove last N chars
}

type ExportConfig struct {
	Fields        []ExportField `json:"fields"`
	CardIDTrimEnd int           `json:"card_id_trim_end"`
}

var builtinPresets = []ExportPreset{
	{Name: "Default", Fields: []ExportField{FieldNormalCount, FieldFoilCount, FieldCardID}},
	{Name: "Riftbound", Fields: []ExportField{FieldCardID, FieldNormalCount, FieldFoilCount}, CardIDTrimEnd: 4},
	{Name: "Simple", Fields: []ExportField{FieldCardID, FieldTotalCount}},
}

func fieldLabel(f ExportField) string {
	switch f {
	case FieldCardID:
		return "Card ID"
	case FieldNormalCount:
		return "Normal Count"
	case FieldFoilCount:
		return "Foil Count"
	case FieldTotalCount:
		return "Total Count"
	}
	return string(f)
}

// trimCardID removes n characters from the end of a card ID. n=0 returns the full ID.
func trimCardID(cardID string, n int) string {
	if n <= 0 || n >= len(cardID) {
		return cardID
	}
	return cardID[:len(cardID)-n]
}

func resolveField(cardID string, normalCount, foilCount int, f ExportField, config ExportConfig) string {
	switch f {
	case FieldCardID:
		return trimCardID(cardID, config.CardIDTrimEnd)
	case FieldNormalCount:
		return strconv.Itoa(normalCount)
	case FieldFoilCount:
		return strconv.Itoa(foilCount)
	case FieldTotalCount:
		return strconv.Itoa(normalCount + foilCount)
	}
	return ""
}

func writeCollectionCSV(db *sql.DB, collectionID int, path string, config ExportConfig) error {
	rows, err := db.Query(`
		SELECT card_id,
		       SUM(CASE WHEN foil = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN foil = 1 THEN 1 ELSE 0 END)
		FROM cards
		WHERE collection_id = ?
		GROUP BY card_id
		ORDER BY card_id ASC
	`, collectionID)
	if err != nil {
		return err
	}
	defer rows.Close()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)

	headers := make([]string, len(config.Fields))
	for i, field := range config.Fields {
		headers[i] = fieldLabel(field)
	}
	w.Write(headers)

	for rows.Next() {
		var cardID string
		var normalCount, foilCount int
		if err := rows.Scan(&cardID, &normalCount, &foilCount); err != nil {
			return err
		}
		row := make([]string, len(config.Fields))
		for i, field := range config.Fields {
			row[i] = resolveField(cardID, normalCount, foilCount, field, config)
		}
		w.Write(row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	w.Flush()
	return w.Error()
}
