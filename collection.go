package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Collection is a user-named custom collection (a grouping of copies).
// Per-game collections are derived from cards.game — see gameCollections.
type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	CardCount int    `json:"card_count"`
}

type GameCollection struct {
	Game      string `json:"game"`
	CardCount int    `json:"card_count"`
	FoilCount int    `json:"foil_count"`
}

func createCollection(db *sql.DB, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("collection name cannot be empty")
	}
	deviceID, _, err := syncDevice(db)
	if err != nil {
		return 0, err
	}
	at := syncNow()
	uid := newUID()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO collections (name, uid, sync_at, sync_by) VALUES (?, ?, ?, ?)`, name, uid, at, deviceID)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := appendSyncEvent(tx, syncEvent{Op: opCollectionCreate, At: at, UID: uid, Name: name}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(id), nil
}

func getCollection(db *sql.DB, id int) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.created_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		WHERE co.id = ?
		GROUP BY co.id
	`, id).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.CardCount)
	return c, err
}

func listCollections(db *sql.DB) ([]Collection, error) {
	rows, err := db.Query(`
		SELECT co.id, co.name, co.created_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.created_at DESC, co.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.CardCount); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

func renameCollection(db *sql.DB, id int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("collection name cannot be empty")
	}
	deviceID, _, err := syncDevice(db)
	if err != nil {
		return err
	}
	at := syncNow()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE collections SET name = ?, sync_at = ?, sync_by = ? WHERE id = ?`, name, at, deviceID, id); err != nil {
		return err
	}
	var uid string
	if err := tx.QueryRow(`SELECT uid FROM collections WHERE id = ?`, id).Scan(&uid); err != nil {
		if err == sql.ErrNoRows {
			return nil // nothing renamed, nothing to record
		}
		return err
	}
	if err := appendSyncEvent(tx, syncEvent{Op: opCollectionRename, At: at, UID: uid, Name: name}); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteCollection removes a custom collection; its copies keep their game
// and simply leave the custom. The explicit UPDATE makes the behavior
// independent of the connection's foreign_keys pragma, and the transaction
// keeps detach + delete + event + tombstone atomic. The copies get no event
// of their own: the delete implies "unfiled" everywhere.
func deleteCollection(db *sql.DB, id int) error {
	at := syncNow()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var uid string
	if err := tx.QueryRow(`SELECT uid FROM collections WHERE id = ?`, id).Scan(&uid); err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.Exec(`UPDATE cards SET collection_id = NULL WHERE collection_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM collections WHERE id = ?`, id); err != nil {
		return err
	}
	if uid != "" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO sync_tombstones (uid, deleted_at) VALUES (?, ?)`, uid, at); err != nil {
			return err
		}
		if err := appendSyncEvent(tx, syncEvent{Op: opCollectionDelete, At: at, UID: uid}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// gameCollections derives the per-game collections: every game the manifests
// know (zero counts included) plus every game present in the DB, sorted, with
// the unknown "" bucket last and only when non-empty.
func gameCollections(db *sql.DB, knownGames []string) ([]GameCollection, error) {
	rows, err := db.Query(`
		SELECT game, COUNT(*), SUM(foil)
		FROM cards
		GROUP BY game
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]GameCollection{}
	for rows.Next() {
		var g GameCollection
		if err := rows.Scan(&g.Game, &g.CardCount, &g.FoilCount); err != nil {
			return nil, err
		}
		counts[g.Game] = g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []GameCollection
	for _, g := range knownGames {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		gc := counts[g]
		gc.Game = g
		out = append(out, gc)
	}
	for g, gc := range counts {
		if g != "" && !seen[g] {
			out = append(out, gc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Game < out[j].Game })
	if other, ok := counts[""]; ok && other.CardCount > 0 {
		out = append(out, other)
	}
	return out, nil
}
