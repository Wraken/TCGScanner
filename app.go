package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context

	model        *Model
	db           *sql.DB
	collectionID int
}

func NewApp() *App {
	return &App{}
}

func (a *App) Close() {
	if a.model != nil {
		a.model.Delete()
	}
	if a.db != nil {
		a.db.Close()
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	db, err := openDB("./collection.db")
	if err != nil {
		fmt.Printf("failed to open database: %v\n", err)
		return
	}
	a.db = db

	last, err := getLastCollection(db)
	switch {
	case err == nil:
		a.collectionID = last.ID
		fmt.Printf("Resuming collection %d\n", last.ID)
	case err == sql.ErrNoRows:
		id, err := createCollection(db)
		if err != nil {
			fmt.Printf("failed to create collection: %v\n", err)
			return
		}
		a.collectionID = id
		fmt.Printf("Collection %d started\n", id)
	default:
		fmt.Printf("failed to get last collection: %v\n", err)
	}
}

func (a *App) ListModels() ([]string, error) {
	dir, err := os.ReadDir("./models")
	if err != nil {
		return nil, err
	}
	models := []string{}
	for _, entry := range dir {
		if entry.IsDir() {
			models = append(models, entry.Name())
		}
	}
	slices.Sort(models)
	return models, nil
}

func (a *App) LoadModel(modelName string) error {
	modelPath := fmt.Sprintf("./models/%s/model.tflite", modelName)
	labelsPath := fmt.Sprintf("./models/%s/labels.json", modelName)

	model, err := NewModel(modelName, modelPath, labelsPath)
	if err != nil {
		return err
	}
	if a.model != nil {
		a.model.Delete()
	}
	a.model = model

	return nil
}

func (a *App) GetCardImage(cardID string) (string, error) {
	if a.model == nil {
		return "", fmt.Errorf("no model loaded")
	}

	for _, ext := range []string{"jpg", "jpeg", "png", "webp"} {
		path := filepath.Join(".", "models", a.model.name, "images", cardID+"."+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			mime := "image/" + ext
			if ext == "jpg" {
				mime = "image/jpeg"
			}
			return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
		}
	}

	return "", fmt.Errorf("image not found for card %q", cardID)
}

func (a *App) GetCurrentCollectionID() int {
	return a.collectionID
}

func (a *App) NewCollection() (Collection, error) {
	if a.db == nil {
		return Collection{}, fmt.Errorf("database not initialized")
	}
	id, err := createCollection(a.db)
	if err != nil {
		return Collection{}, err
	}
	a.collectionID = id
	fmt.Printf("Switched to new collection %d\n", id)
	return getCollection(a.db, id)
}

func (a *App) SetCollection(id int) {
	a.collectionID = id
	fmt.Printf("Switched to collection %d\n", id)
}

func (a *App) RenameCollection(id int, name string) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return renameCollection(a.db, id, name)
}

func (a *App) GetCollectionCards(collectionID int) ([]CollectionCard, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCollectionCards(a.db, collectionID)
}

func (a *App) DeleteCard(id int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return deleteCard(a.db, id)
}

func (a *App) SaveCard(cardID string, foil bool) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return insertCard(a.db, a.collectionID, cardID, foil)
}

func (a *App) GetCollections() ([]Collection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCollections(a.db)
}

func (a *App) ExportCollectionCSV(collectionID int) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("database not initialized")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fmt.Sprintf("collection_%d.csv", collectionID),
		Filters: []runtime.FileFilter{
			{DisplayName: "CSV Files (*.csv)", Pattern: "*.csv"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeCollectionCSV(a.db, collectionID, path); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) Predict(imageData string) (*Prediction, error) {
	if a.model == nil {
		return nil, fmt.Errorf("no model loaded")
	}

	pred, err := a.model.Predict(imageData)
	if err != nil {
		runtime.LogDebugf(a.ctx, "[Predict] Error: %v", err)
		return nil, err
	}

	fmt.Println("[Predict] card_id=%s confidence=%.4f", pred.CardID, pred.Confidence)
	return pred, nil
}
