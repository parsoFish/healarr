package config

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
)

// ReadArrAPIKey extracts <ApiKey> from a Sonarr/Radarr/Prowlarr config.xml.
func ReadArrAPIKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		APIKey string `xml:"ApiKey"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.APIKey == "" {
		return "", errors.New("no <ApiKey> element in " + path)
	}
	return doc.APIKey, nil
}
