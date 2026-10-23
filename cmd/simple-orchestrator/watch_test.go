package main

// Tests must never open real terminal windows.
func init() {
	terminalOpener = nil
}
