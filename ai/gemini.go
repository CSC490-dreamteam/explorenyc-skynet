package ai

import (
	"context"
	"os"

	"google.golang.org/genai"
)

type GeminiClient struct {
	Client *genai.Client
	Model  string
}

// initializes a client for a specific model (e.g., "gemini-2.0-flash")
func NewClient(ctx context.Context, modelName string) (*GeminiClient, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGoogleAI,
	})
	if err != nil {
		return nil, err
	}

	return &GeminiClient{
		Client: client,
		Model:  modelName,
	}, nil
}

// Generate is a simple wrapper to get text out of the model
func (g *GeminiClient) Generate(ctx context.Context, prompt string) (string, error) {
	result, err := g.Client.Models.GenerateContent(ctx, g.Model, genai.Text(prompt), nil)
	if err != nil {
		return "", err
	}

	//grab the first candidate's text
	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", nil
}
