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

	modelMu        sync.RWMutex
	model          Predictor
	db             *sql.DB
	activeCustomID int
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

	// Cards scanned before their game's images were installed heal on start.
	if n, err := backfillCardGames(db, gameResolver(allModelImagesDirs())); err != nil {
		fmt.Printf("game backfill failed: %v\n", err)
	} else if n > 0 {
		fmt.Printf("game backfill: %d copies resolved\n", n)
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

func (a *App) SetActiveCustom(id int) {
	a.activeCustomID = id
}

func (a *App) GetActiveCustom() int {
	return a.activeCustomID
}

func (a *App) GetGameCollections() ([]GameCollection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var known []string
	for _, dir := range allModelImagesDirs() {
		known = append(known, manifestGames(dir)...)
	}
	return gameCollections(a.db, known)
}

func (a *App) GetCustomCollections() ([]Collection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCollections(a.db)
}

func (a *App) NewCustomCollection(name string) (Collection, error) {
	if a.db == nil {
		return Collection{}, fmt.Errorf("database not initialized")
	}
	id, err := createCollection(a.db, name)
	if err != nil {
		return Collection{}, err
	}
	return getCollection(a.db, id)
}

func (a *App) RenameCollection(id int, name string) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return renameCollection(a.db, id, name)
}

func (a *App) DeleteCollection(id int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	if a.activeCustomID == id {
		a.activeCustomID = 0
	}
	return deleteCollection(a.db, id)
}

func (a *App) GetCards(f CardFilter) ([]CollectionCard, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCards(a.db, f)
}

func (a *App) MoveCardToCustom(rowID, customID int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return moveCardToCustom(a.db, rowID, customID)
}

func (a *App) UpdateCard(card CollectionCard) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	// The id may have been corrected by hand — re-resolve the game.
	card.Game = gameResolver(allModelImagesDirs())(card.CardID)
	return updateCard(a.db, card)
}

func (a *App) DeleteCard(id int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return deleteCard(a.db, id)
}

func (a *App) SaveCard(cardID string, foil bool) (CollectionCard, error) {
	if a.db == nil {
		return CollectionCard{}, fmt.Errorf("database not initialized")
	}
	model := a.currentModel()
	if model == nil {
		return CollectionCard{}, fmt.Errorf("no model loaded")
	}
	game := gameOfCard(modelImagesDir(model.Name()), cardID)
	id, err := insertCard(a.db, a.activeCustomID, cardID, game, model.Name(), foil)
	if err != nil {
		return CollectionCard{}, err
	}
	return getCard(a.db, id)
}

func (a *App) GetCardImageByModel(cardID, modelName string) (string, error) {
	if modelName == "" {
		return "", fmt.Errorf("no model name provided")
	}
	return loadCardImage(cardID, modelName)
}

func (a *App) ListExportPresets() []ExportPreset {
	return builtinPresets
}

func (a *App) ExportCSV(f CardFilter, config ExportConfig) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("database not initialized")
	}
	if len(config.Fields) == 0 {
		config = ExportConfig{Fields: builtinPresets[0].Fields}
	}
	name := f.Game
	if f.OtherBucket {
		name = "autres"
	}
	if f.CollectionID != 0 {
		if c, err := getCollection(a.db, f.CollectionID); err == nil {
			name = c.Name
		}
	}
	if name == "" {
		name = "export"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fmt.Sprintf("%s.csv", name),
		Filters: []runtime.FileFilter{
			{DisplayName: "CSV Files (*.csv)", Pattern: "*.csv"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeCardsCSV(a.db, f, path, config); err != nil {
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
