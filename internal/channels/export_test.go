package channels

// ResetForTest clears the channel registry. Only for use in tests.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Factory{}
}
