package agent

// Windows is used for control-plane development/tests, never for Linux nodes.
// FlushFileBuffers on a directory is not supported; file content is still synced.
func syncDirectory(string) error { return nil }
