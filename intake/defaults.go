package intake

// Guidance resolves operator-supplied static guidance. Inline content wins
// over a file. There is deliberately no built-in policy: deployments decide
// what content is acceptable, and private guidance must not leak into the
// portable application.
func Guidance(inline, path string, readFile func(string) ([]byte, error)) string {
	if inline != "" {
		return inline
	}
	if path != "" && readFile != nil {
		if data, err := readFile(path); err == nil {
			return string(data)
		}
	}
	return ""
}
