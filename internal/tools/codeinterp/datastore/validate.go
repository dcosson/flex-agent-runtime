package datastore

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ValidateKey(key string, maxLen int) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if maxLen > 0 && len(key) > maxLen {
		return fmt.Errorf("key too long")
	}
	if strings.Contains(key, "\x00") {
		return fmt.Errorf("null byte in key")
	}
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, "\\") {
		return fmt.Errorf("absolute keys are not allowed")
	}
	clean := filepath.Clean(key)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "..\\") {
		return fmt.Errorf("path traversal is not allowed")
	}
	if strings.Contains(key, "..") {
		parts := strings.FieldsFunc(key, func(r rune) bool { return r == '/' || r == '\\' })
		for _, p := range parts {
			if p == ".." {
				return fmt.Errorf("path traversal is not allowed")
			}
		}
	}
	return nil
}
