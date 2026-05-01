package executorapi

// Gemini support is deferred. See clientFor() in client.go — the "gemini"
// case returns a PermanentError pointing at the Phase E follow-up ticket.
// When a consumer profile lands and we're ready to take on the
// google.golang.org/genai dep weight (Azure + AWS + cloud.google.com auth
// chains, ~80 transitive modules), implement geminiClient here mirroring
// the anthropic.go / openai.go shape.
