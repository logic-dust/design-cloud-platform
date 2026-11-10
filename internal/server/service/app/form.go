package app

import (
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
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
		return contract.NewValidationError("name must match [A-Za-z0-9_] and be 1-32 characters")
	}
	if utf8.RuneCountInString(form.Alias) > maxAliasRuneCount {
		return contract.NewValidationError("alias must be at most 100 characters")
	}

	switch form.Type {
	case model.BatchApp, model.GUIApp:
		return nil
	case model.FormApp:
		if strings.TrimSpace(form.Form) == "" {
			return contract.NewInvalidParamError("form app must have a form")
		}
		if !json.Valid([]byte(form.Form)) {
			return contract.NewValidationError("form app must provide valid JSON form")
		}
		return nil
	default:
		return contract.NewValidationError("invalid app type " + string(form.Type))
	}
}
