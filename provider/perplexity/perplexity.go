// Package perplexity provides Perplexity's LLM services: the Agent API, which
// answers with web search over the OpenAI Responses protocol, and the Sonar
// chat completions API, which Perplexity no longer supports.
package perplexity

const (
	// baseURL is the Sonar chat completions base. It has no "/v1" path segment,
	// unlike most compatible providers.
	baseURL      = "https://api.perplexity.ai"
	defaultModel = "sonar"
)
