package command

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/desktop"
)

func runDesktopCommand(_ *Flags, args []string) error {
	if len(args) == 0 {
		printDesktopUsage()
		return fmt.Errorf("desktop sub-command required: plugin")
	}
	switch args[0] {
	case "plugin":
		return runDesktopPluginCommand(args[1:])
	default:
		printDesktopUsage()
		return fmt.Errorf("unknown desktop command: %s", args[0])
	}
}

func printDesktopUsage() {
	console.PrintStderr("Usage: %s desktop plugin <publish|verify> [options]\n\n", config.AppName)
	console.PrintStderr("Subcommands:\n")
	console.PrintStderr("  publish                  Write latest.zip + latest.version under data/releases/desktop/plugins\n")
	console.PrintStderr("  verify                   Validate an existing plugin release directory\n")
}

func runDesktopPluginCommand(args []string) error {
	if len(args) == 0 {
		printDesktopUsage()
		return fmt.Errorf("desktop plugin sub-command required: publish, verify")
	}
	switch args[0] {
	case "publish":
		return runDesktopPluginPublish(args[1:])
	case "verify":
		return runDesktopPluginVerify(args[1:])
	default:
		printDesktopUsage()
		return fmt.Errorf("unknown desktop plugin command: %s", args[0])
	}
}

func runDesktopPluginPublish(args []string) error {
	fs := newCommandFlagSet("publish", os.Stderr)

	var name, platform, version, dest, fromDir, zipPath string
	fs.StringVar(&name, "name", "", "plugin name (e.g. turbovnc)")
	fs.StringVar(&platform, "platform", "", "platform channel: win, linux, osx")
	fs.StringVar(&version, "version", "", "release version (optional if VERSION.txt exists in --from-dir)")
	fs.StringVar(&dest, "dest", "data/releases/desktop/plugins", "plugins root directory")
	fs.StringVar(&fromDir, "from-dir", "", "stage directory to zip as latest.zip")
	fs.StringVar(&zipPath, "zip", "", "existing zip to copy as latest.zip (mutually exclusive with --from-dir)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(platform) == "" {
		return fmt.Errorf("--name and --platform are required")
	}
	if fromDir != "" && zipPath != "" {
		return fmt.Errorf("use either --from-dir or --zip, not both")
	}
	if fromDir == "" && zipPath == "" {
		return fmt.Errorf("one of --from-dir or --zip is required")
	}

	validName, err := desktop.ValidatePluginName(name)
	if err != nil {
		return err
	}

	channel, ok := desktop.CanonicalChannel(platform)
	if !ok {
		return fmt.Errorf("unsupported --platform %q (want win|windows, linux, osx|macos|darwin)", platform)
	}

	outDir := filepath.Join(dest, validName, channel)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	targetZip := filepath.Join(outDir, desktop.LatestZipFile)
	if fromDir != "" {
		if version == "" {
			versionPath := filepath.Join(fromDir, "VERSION.txt")
			data, err := os.ReadFile(versionPath)
			if err != nil {
				return fmt.Errorf("--version required or VERSION.txt in --from-dir: %w", err)
			}
			version = strings.TrimSpace(string(data))
		}
		if err := zipDirectory(fromDir, targetZip); err != nil {
			return err
		}
	} else {
		if version == "" {
			return fmt.Errorf("--version is required with --zip")
		}
		if err := copyFile(zipPath, targetZip); err != nil {
			return err
		}
	}

	sum, size, err := fileSHA256AndSize(targetZip)
	if err != nil {
		return err
	}

	metaLine := desktop.FormatLatestVersionLine(version, sum, size)
	metaData := []byte(metaLine + "\n")
	metaPath := filepath.Clean(filepath.Join(outDir, desktop.LatestVersionFile))
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		return fmt.Errorf("write %s: %w", desktop.LatestVersionFile, err)
	}

	console.PrintStderr("Published %s %s -> %s\n", validName, channel, outDir)
	console.PrintStderr("  %s\n", metaLine)
	return nil
}

func runDesktopPluginVerify(args []string) error {
	fs := newCommandFlagSet("verify", os.Stderr)

	var name, platform, dest string
	fs.StringVar(&name, "name", "", "plugin name")
	fs.StringVar(&platform, "platform", "", "platform channel")
	fs.StringVar(&dest, "dest", "data/releases/desktop/plugins", "plugins root directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(platform) == "" {
		return fmt.Errorf("--name and --platform are required")
	}

	validName, err := desktop.ValidatePluginName(name)
	if err != nil {
		return err
	}

	channel, ok := desktop.CanonicalChannel(platform)
	if !ok {
		return fmt.Errorf("unsupported --platform %q (want win|windows, linux, osx|macos|darwin)", platform)
	}

	dir := filepath.Join(dest, validName, channel)
	metaBytes, err := os.ReadFile(filepath.Join(dir, desktop.LatestVersionFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", desktop.LatestVersionFile, err)
	}
	version, sha256Hex, size, err := desktop.ParseLatestVersionLine(string(metaBytes))
	if err != nil {
		return err
	}

	zipPath := filepath.Join(dir, desktop.LatestZipFile)
	sum, actualSize, err := fileSHA256AndSize(zipPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, sha256Hex) {
		return fmt.Errorf("sha256 mismatch: meta=%s actual=%s", sha256Hex, sum)
	}
	if size > 0 && actualSize != size {
		return fmt.Errorf("size mismatch: meta=%d actual=%d", size, actualSize)
	}

	console.PrintStderr("OK %s %s version=%s size=%d\n", validName, platform, version, actualSize)
	return nil
}

func zipDirectory(sourceDir, destZip string) error {
	if err := os.RemoveAll(destZip); err != nil && !os.IsNotExist(err) {
		return err
	}

	out, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	sourceDir = filepath.Clean(sourceDir)
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func fileSHA256AndSize(path string) (hexDigest string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}
