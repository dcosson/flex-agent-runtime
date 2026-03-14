package tests

import "os"

func writeFileWithPerm(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
