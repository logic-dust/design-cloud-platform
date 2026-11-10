package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxConnectionResourceSlugLen matches ConnectionResource.Slug varchar(32).
	MaxConnectionResourceSlugLen = 32
	// MaxConnectionResourceNameLen matches ConnectionResource.Name varchar(32).
	MaxConnectionResourceNameLen = 32
	// MaxConnectionResourceDescriptionLen matches ConnectionResource.Description varchar(256).
	MaxConnectionResourceDescriptionLen = 256
)

var connectionResourceSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidateConnectionResourceSlug checks the unique resource slug (lowercase ASCII).
func ValidateConnectionResourceSlug(slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("resource slug is required")
	}
	if utf8.RuneCountInString(slug) > MaxConnectionResourceSlugLen {
		return fmt.Errorf("resource slug cannot exceed %d characters", MaxConnectionResourceSlugLen)
	}
	if !connectionResourceSlugPattern.MatchString(slug) {
		return fmt.Errorf("invalid resource slug %q: must be lowercase ASCII slug", slug)
	}
	return nil
}

// ValidateConnectionResourceName checks optional human-facing display name.
func ValidateConnectionResourceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if utf8.RuneCountInString(name) > MaxConnectionResourceNameLen {
		return fmt.Errorf("name cannot exceed %d characters", MaxConnectionResourceNameLen)
	}
	if strings.ContainsAny(name, "\x00\r\n\t") {
		return fmt.Errorf("name contains invalid control characters")
	}
	return nil
}

// ValidateConnectionResourceDescription checks optional description text.
func ValidateConnectionResourceDescription(description string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil
	}
	if utf8.RuneCountInString(description) > MaxConnectionResourceDescriptionLen {
		return fmt.Errorf("description cannot exceed %d characters", MaxConnectionResourceDescriptionLen)
	}
	if strings.ContainsAny(description, "\x00\r\n\t") {
		return fmt.Errorf("description contains invalid control characters")
	}
	return nil
}
