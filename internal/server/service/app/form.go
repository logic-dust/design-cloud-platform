package app

import (
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/infra/jsoncodec"
	"regexp"
	"strings"
	"unicode/utf8"

	"design-cloud-engine/internal/model"
)

const maxAppNameRuneCount = 100

var appSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func applyAppForm(app *model.App, form model.AppBase) {
	app.AppBase = form
}

func validateReqAppForm(form model.AppBase) error {
	if !appSlugPattern.MatchString(form.Slug) {
		return contract.NewValidationError("slug must match [A-Za-z0-9_] and be 1-32 characters")
	}
	if utf8.RuneCountInString(form.Name) > maxAppNameRuneCount {
		return contract.NewValidationError("name must be at most 100 characters")
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
