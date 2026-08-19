package command

import (
	"fmt"
	"os"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
)

func runGenConfigCommand(_ *Flags, _ []string) error {
	const filename = "config.example.toml"
	text, err := config.GetExampleContent()
	if err != nil {
		return fmt.Errorf("failed to render %s: %w", filename, err)
	}

	if err := os.WriteFile(filename, []byte(text), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}

	common.PrintStdout("Generated example config at: %s\n", filename)
	return nil
}
