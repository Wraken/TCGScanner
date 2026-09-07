package main

// CardLanguage is one printed language a copy can be recorded in. The list
// is the single source of truth: the frontend fetches it (ListLanguages)
// and every binding that stores a language validates against it.
type CardLanguage struct {
	Code  string `json:"code"`  // ISO 639-1, lowercase
	Label string `json:"label"` // English name shown in the UI
}

// defaultLanguage stamps copies when nothing else was chosen (existing rows
// migrate to it too: the collection was English-only before 2026-09-07).
const defaultLanguage = "en"

var cardLanguages = []CardLanguage{
	{"en", "English"}, {"fr", "French"}, {"de", "German"}, {"es", "Spanish"},
	{"it", "Italian"}, {"pt", "Portuguese"}, {"ja", "Japanese"}, {"ko", "Korean"},
	{"zh", "Chinese"},
}

// validLanguage reports whether code is one of cardLanguages. Exact match:
// no case folding, no aliases — the UI only ever sends codes from the list.
func validLanguage(code string) bool {
	for _, l := range cardLanguages {
		if l.Code == code {
			return true
		}
	}
	return false
}
