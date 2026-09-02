package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const campaignQRCodeSize = 512

var errClipboardUnsupported = errors.New("clipboard is not supported on this platform")

func campaignQRCodePNG(rawURL string) ([]byte, error) {
	return qrcode.Encode(rawURL, qrcode.Medium, campaignQRCodeSize)
}

func writeCampaignQRCode(path, rawURL string) error {
	png, err := campaignQRCodePNG(rawURL)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("QR output already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, png, 0644)
}

func copyTextToClipboard(value string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "pbcopy"
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			command = "wl-copy"
		} else if _, err := exec.LookPath("xclip"); err == nil {
			command, args = "xclip", []string{"-selection", "clipboard"}
		} else {
			return errors.New("clipboard is unavailable; install wl-copy or xclip")
		}
	default:
		return errClipboardUnsupported
	}
	return runClipboardCommand(command, args, []byte(value))
}

func copyPNGToClipboard(png []byte) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			command, args = "wl-copy", []string{"--type", "image/png"}
		} else if _, err := exec.LookPath("xclip"); err == nil {
			command, args = "xclip", []string{"-selection", "clipboard", "-t", "image/png", "-i"}
		} else {
			return errors.New("image clipboard is unavailable; install wl-copy or xclip")
		}
	case "darwin":
		file, err := os.CreateTemp("", "hs-campaign-qr-*.png")
		if err != nil {
			return err
		}
		path := file.Name()
		defer os.Remove(path)
		if _, err := file.Write(png); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		command = "osascript"
		args = []string{"-e", fmt.Sprintf("set the clipboard to (read POSIX file %q as «class PNGf»)", path)}
	default:
		return errClipboardUnsupported
	}
	return runClipboardCommand(command, args, png)
}

func runClipboardCommand(command string, args []string, input []byte) error {
	cmd := exec.Command(command, args...)
	cmd.Stdin = bytes.NewReader(input)
	if output, err := cmd.CombinedOutput(); err != nil {
		if len(output) > 0 {
			return fmt.Errorf("clipboard command failed: %s", strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("clipboard command failed: %w", err)
	}
	return nil
}
