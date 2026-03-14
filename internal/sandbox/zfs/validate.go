package zfs

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	datasetNamePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-:/]*$`)
	snapshotNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$`)
	holdTagPattern      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$`)
	propertyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:_.-]*$`)
)

func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	if len(name) > 1024 {
		return fmt.Errorf("%w: name exceeds 1024 characters", ErrInvalidName)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("%w: path traversal not allowed", ErrInvalidName)
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("%w: name cannot start or end with '/'", ErrInvalidName)
	}
	if !datasetNamePattern.MatchString(name) {
		return fmt.Errorf("%w: invalid characters in %q", ErrInvalidName, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" {
			return fmt.Errorf("%w: empty path component", ErrInvalidName)
		}
		if len(part) > 255 {
			return fmt.Errorf("%w: component exceeds 255 characters", ErrInvalidName)
		}
	}
	return nil
}

func ValidateSnapshotName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty snapshot name", ErrInvalidName)
	}
	if len(name) > 255 {
		return fmt.Errorf("%w: snapshot name exceeds 255 characters", ErrInvalidName)
	}
	if !snapshotNamePattern.MatchString(name) {
		return fmt.Errorf("%w: invalid snapshot name %q", ErrInvalidName, name)
	}
	return nil
}

func ValidateSnapshotFullName(name string) error {
	parts := strings.SplitN(name, "@", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: full snapshot name must contain one '@'", ErrInvalidName)
	}
	if err := ValidateName(parts[0]); err != nil {
		return err
	}
	return ValidateSnapshotName(parts[1])
}

func ValidatePoolName(name string) error {
	if strings.Contains(name, "/") {
		return fmt.Errorf("%w: pool name cannot contain '/'", ErrInvalidName)
	}
	return ValidateName(name)
}

func ValidateHoldTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("%w: empty hold tag", ErrInvalidName)
	}
	if len(tag) > 255 {
		return fmt.Errorf("%w: hold tag exceeds 255 characters", ErrInvalidName)
	}
	if !holdTagPattern.MatchString(tag) {
		return fmt.Errorf("%w: invalid hold tag %q", ErrInvalidName, tag)
	}
	return nil
}

func ValidatePropertyName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty property name", ErrInvalidName)
	}
	if !propertyNamePattern.MatchString(name) {
		return fmt.Errorf("%w: invalid property name %q", ErrInvalidName, name)
	}
	dangerous := map[string]struct{}{"exec": {}, "setuid": {}, "devices": {}}
	if _, ok := dangerous[name]; ok {
		return fmt.Errorf("%w: restricted property %q", ErrInvalidName, name)
	}
	return nil
}

func ValidateMountpoint(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty mountpoint", ErrInvalidName)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: mountpoint must be absolute", ErrInvalidName)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("%w: mountpoint traversal not allowed", ErrInvalidName)
	}
	return nil
}

func ValidateDevicePath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty device path", ErrInvalidName)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: device path must be absolute", ErrInvalidName)
	}
	return nil
}
