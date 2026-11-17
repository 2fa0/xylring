package main

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
)

//go:embed locales/en.json
var englishJSON []byte

var englishText = func() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(englishJSON, &messages); err != nil {
		panic(err)
	}
	return messages
}()

// Workers can format status messages while the UI changes language.
var englishUI atomic.Bool

func tr(text string) string {
	if englishUI.Load() {
		if translated, ok := englishText[text]; ok {
			return translated
		}
	}
	return text
}

func validLanguage(language string) bool {
	return language == "" || language == "zh-Hans" || language == "en"
}

func (a *app) setLanguage(language string) {
	if !validLanguage(language) {
		return
	}
	a.language = language
	if language != "" {
		englishUI.Store(language == "en")
	}
	mygo.App.SetLocale(language)
}

func (a *app) configureLanguages() {
	mygo.App.SetLanguages("en", "zh-Hans")
	mygo.App.OnLocaleChanged(func(locale string) {
		englishUI.Store(strings.HasPrefix(locale, "en"))
		if a.menu != nil {
			a.installMenu()
		}
	})
	a.setLanguage(a.language)
	englishUI.Store(strings.HasPrefix(mygo.App.Locale(), "en"))
}
