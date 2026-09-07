package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context

	modelMu sync.RWMutex
	model   Predictor
	db      *sql.DB

	// Bound methods may run on concurrent goroutines — atomic keeps the
	// check-then-clear in DeleteCollection from racing SaveCard.
	activeCustomID atomic.Int32

	// Printed language stamped on new scans. Sticky like activeCustomID and
	// in-memory only (resets to defaultLanguage on every start). "" means
	// defaultLanguage. Bound methods run concurrently: guard with the mutex.
	scanLangMu sync.Mutex
	scanLang   string
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
	a.activeCustomID.Store(int32(id))
}

func (a *App) GetActiveCustom() int {
	return int(a.activeCustomID.Load())
}

// ListLanguages is the closed list of printed languages a copy can carry.
func (a *App) ListLanguages() []CardLanguage {
	return slices.Clone(cardLanguages)
}

// SetScanLang chooses the language stamped on the next scans. An unknown
// code is rejected and leaves the current choice untouched.
func (a *App) SetScanLang(code string) error {
	if !validLanguage(code) {
		return fmt.Errorf("unknown language %q", code)
	}
	a.scanLangMu.Lock()
	a.scanLang = code
	a.scanLangMu.Unlock()
	return nil
}

func (a *App) GetScanLang() string {
	a.scanLangMu.Lock()
	defer a.scanLangMu.Unlock()
	if a.scanLang == "" {
		return defaultLanguage
	}
	return a.scanLang
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
	if err := deleteCollection(a.db, id); err != nil {
		return err
	}
	a.activeCustomID.CompareAndSwap(int32(id), 0)
	return nil
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
	if !validLanguage(card.Lang) {
		return fmt.Errorf("unknown language %q", card.Lang)
	}
	prev, err := getCard(a.db, card.ID)
	if err != nil {
		return err
	}
	// A hand-corrected id gets its game re-resolved; any other edit keeps the
	// stored game so a foil toggle can never downgrade it (shrunk manifest,
	// bogus Game field from the caller).
	if card.CardID != prev.CardID {
		card.Game = gameResolver(allModelImagesDirs())(card.CardID)
	} else {
		card.Game = prev.Game
	}
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
	// Resolve across every installed manifest, like UpdateCard does — a
	// hand-typed id known only to another model's images still gets a game.
	game := gameResolver(allModelImagesDirs())(cardID)
	id, err := insertCard(a.db, int(a.activeCustomID.Load()), cardID, game, model.Name(), foil, a.GetScanLang())
	if err != nil {
		return CollectionCard{}, err
	}
	return getCard(a.db, id)
}

// GetGameCover serves the optional bundle-provided cover art of a game
// (images/<game>/_cover.<ext>); the frontend falls back to its built-in
// artwork when this errors.
func (a *App) GetGameCover(game string) (string, error) {
	return loadGameCoverFrom(allModelImagesDirs(), game)
}

// GetCardInfo returns the bundle metadata of a card (name, set, rarity...).
// Errors when the bundle has no metadata for it; the details dialog then
// simply shows the id.
func (a *App) GetCardInfo(cardID string) (CardInfo, error) {
	return lookupCardInfo(allModelImagesDirs(), allModelMetadataDirs(), cardID)
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
	// Reject an invalid filter before showing a dead-end save dialog.
	if _, _, err := cardFilterWhere(f); err != nil {
		return "", err
	}
	if len(config.Fields) == 0 {
		config = ExportConfig{Fields: builtinPresets[0].Fields}
	}
	name := f.Game
	if f.OtherBucket {
		name = "other"
	}
	if f.CollectionID != 0 {
		if c, err := getCollection(a.db, f.CollectionID); err == nil {
			name = c.Name
		}
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fmt.Sprintf("%s.csv", safeFilename(name)),
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
