package harnesstest

import "os"

func tempDir() (string, error) { return os.MkdirTemp("", "harnesstest-") }

func removeAll(dir string) { _ = os.RemoveAll(dir) }
