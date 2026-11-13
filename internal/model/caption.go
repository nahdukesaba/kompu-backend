package model

// TextGenerationRequest is provider-neutral: any AI adapter can serve it.
type TextGenerationRequest struct {
	System    string
	User      string
	MaxTokens int
}
