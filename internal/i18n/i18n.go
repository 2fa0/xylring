package i18n

import (
	_ "embed"
	"encoding/json"
	"sync/atomic"
)

//go:embed locales/en.json
var englishJSON []byte

// EnglishText maps Chinese source strings to their English translations.
var EnglishText = func() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(englishJSON, &messages); err != nil {
		panic(err)
	}
	return messages
}()

// EnglishUI controls whether English UI mode is active.
// Workers can format status messages while the UI changes language.
var EnglishUI atomic.Bool

// Tr translates a UI string to English when English mode is active.
func Tr(text string) string {
	if EnglishUI.Load() {
		if translated, ok := EnglishText[text]; ok {
			return translated
		}
	}
	return text
}
