package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context

	modelMu      sync.RWMutex
	model        Predictor
	db           *sql.DB
	collectionID int
}

func NewApp() *App {
	return &App{}
}

func (a *App) Close() {
	a.modelMu.Lock()
	model := a.model
	a.model = nil
	a.modelMu.Unlock()
	if model != nil {
		model.Delete()
	}
	if a.db != nil {
		a.db.Close()
	}
}

// currentModel returns the loaded model under the read lock. Predictor is an
// interface value, so an unsynchronised read racing with LoadModel's swap
// could observe a torn (type, pointer) pair. Callers that only need cheap
// metadata such as Name may use the result outside the lock; anything that
// runs inference must hold modelMu for the whole call, as Predict does.
func (a *App) currentModel() Predictor {
	a.modelMu.RLock()
	defer a.modelMu.RUnlock()
	return a.model
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
	dir := filepath.Join(".", "models", modelName)

	// Build new model before acquiring the lock (model init is slow).
	// A directory holding embedding_model.onnx uses the YOLO + embedding
	// pipeline; anything else is a legacy TFLite classifier.
	var model Predictor
	var err error
	if isEmbeddingModel(dir) {
		model, err = NewEmbeddingModel(modelName, dir)
	} else {
		model, err = NewModel(modelName,
			filepath.Join(dir, "model.tflite"),
			filepath.Join(dir, "labels.json"))
	}
	if err != nil {
		return err
	}

	a.modelMu.Lock()
	old := a.model
	a.model = model
	a.modelMu.Unlock()

	// Delete old model only after the lock is released and no Predict can use it
	if old != nil {
		old.Delete()
	}

	return nil
}

func (a *App) GetCardImage(cardID string) (string, error) {
	model := a.currentModel()
	if model == nil {
		return "", fmt.Errorf("no model loaded")
	}
	return loadCardImage(cardID, model.Name())
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

func (a *App) UpdateCard(card CollectionCard) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return updateCard(a.db, card)
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
	modelName := ""
	if model := a.currentModel(); model != nil {
		modelName = model.Name()
	}
	return insertCard(a.db, a.collectionID, cardID, modelName, foil)
}

func (a *App) GetCardImageByModel(cardID, modelName string) (string, error) {
	if modelName == "" {
		return "", fmt.Errorf("no model name provided")
	}
	return loadCardImage(cardID, modelName)
}

func (a *App) GetCollections() ([]Collection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCollections(a.db)
}

func (a *App) ListExportPresets() []ExportPreset {
	return builtinPresets
}

func (a *App) ExportCollectionCSV(collectionID int, config ExportConfig) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("database not initialized")
	}
	if len(config.Fields) == 0 {
		config = ExportConfig{Fields: builtinPresets[0].Fields}
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
	if err := writeCollectionCSV(a.db, collectionID, path, config); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) Predict(imageData string) (*Prediction, error) {
	a.modelMu.RLock()
	defer a.modelMu.RUnlock()

	if a.model == nil {
		return nil, fmt.Errorf("no model loaded")
	}

	t0 := time.Now()
	pred, err := a.model.Predict(imageData)
	goMs := time.Since(t0).Milliseconds()
	if err != nil {
		runtime.LogDebugf(a.ctx, "[Predict] Error: %v", err)
		return nil, err
	}

	if pred.DetectScore > 0 {
		fmt.Printf("[Predict] card_id=%s confidence=%.4f margin=%.4f detect=%.2f confident=%t go=%dms payload=%dKB\n",
			pred.CardID, pred.Confidence, pred.Margin, pred.DetectScore, pred.Confident, goMs, len(imageData)/1024)
	} else {
		fmt.Printf("[Predict] card_id=%s confidence=%.4f confident=%t go=%dms payload=%dKB\n",
			pred.CardID, pred.Confidence, pred.Confident, goMs, len(imageData)/1024)
	}
	return pred, nil
}
