package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignQRCodePNG(t *testing.T) {
	png, err := campaignQRCodePNG("https://example.test/articles/entry/?utm_campaign=always-on")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("QR output is not a PNG: %x", png[:min(len(png), 8)])
	}
}

func TestWriteCampaignQRCodeDoesNotOverwrite(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "campaign-qr.png")
	if err := writeCampaignQRCode(path, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCampaignQRCode(path, "https://example.test/other"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, updated) {
		t.Fatal("existing QR file was changed")
	}
}

func TestCampaignValuesAcceptsClipboardFlags(t *testing.T) {
	values, positional, err := campaignValues([]string{"content/articles/entry.md", "--copy-link", "--qr-file", "campaign-qr.png", "--copy-qr"}, map[string]bool{"copy-link": true, "qr-file": true, "copy-qr": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(positional) != 1 || values["copy-link"] != "true" || values["qr-file"] != "campaign-qr.png" || values["copy-qr"] != "true" {
		t.Fatalf("values=%#v positional=%#v", values, positional)
	}
}
