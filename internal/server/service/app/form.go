package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	"design-cloud-engine/internal/model"
)

const maxAliasRuneCount = 100

var appNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func applyAppForm(app *model.App, form model.AppBase) {
	app.AppBase = form
}

func validateReqAppForm(form model.AppBase) error {
	if !appNamePattern.MatchString(form.Name) {
		return fmt.Errorf("name must match [A-Za-z0-9_] and be 1-32 characters: %w", fs.ErrInvalid)
	}
	if utf8.RuneCountInString(form.Alias) > maxAliasRuneCount {
		return fmt.Errorf("alias must be at most %d characters: %w", maxAliasRuneCount, fs.ErrInvalid)
	}

	switch form.Type {
	case model.BatchApp, model.GUIApp:
		return nil
	case model.FormApp:
		if strings.TrimSpace(form.Form) == "" {
			return fmt.Errorf("form app must have a form: %w", fs.ErrInvalid)
		}
		if !json.Valid([]byte(form.Form)) {
			return fmt.Errorf("form app must provide valid JSON form: %w", fs.ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("invalid app type %q: %w", form.Type, fs.ErrInvalid)
	}
}
