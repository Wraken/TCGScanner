package main

import "database/sql"

type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	StartedAt string `json:"started_at"`
	CardCount int    `json:"card_count"`
}

func createCollection(db *sql.DB) (int, error) {
	res, err := db.Exec(`INSERT INTO collections DEFAULT VALUES`)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func getLastCollection(db *sql.DB) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.started_at DESC
		LIMIT 1
	`).Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount)
	return c, err
}

func getCollection(db *sql.DB, id int) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		WHERE co.id = ?
		GROUP BY co.id
	`, id).Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount)
	return c, err
}

func listCollections(db *sql.DB) ([]Collection, error) {
	rows, err := db.Query(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.started_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

func renameCollection(db *sql.DB, id int, name string) error {
	_, err := db.Exec(`UPDATE collections SET name = ? WHERE id = ?`, name, id)
	return err
}
