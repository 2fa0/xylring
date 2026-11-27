package app

import (
	"strings"

	"github.com/2fa0/xylring/internal/i18n"
	"github.com/egoist/mygo"
)

var englishText = i18n.EnglishText
var englishUI = &i18n.EnglishUI

func tr(text string) string {
	return i18n.Tr(text)
}

func trMessage(text string) string {
	return i18n.TrMessage(text)
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
