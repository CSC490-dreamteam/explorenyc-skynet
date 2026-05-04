package ai

import (
	"context"
	"fmt"
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
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	return &GeminiClient{Client: client, Model: modelName}, nil
}

// regular prompt
func (g *GeminiClient) Prompt(ctx context.Context, prompt string) (string, error) {
	result, err := g.Client.Models.GenerateContent(ctx, g.Model, genai.Text(prompt), nil)
	if err != nil {
		return "", fmt.Errorf("generate failed: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

// GenerateJSON sends a prompt and forces a JSON response
func (g *GeminiClient) PromptJSON(ctx context.Context, prompt string) (string, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
	}

	result, err := g.Client.Models.GenerateContent(ctx, g.Model, genai.Text(prompt), config)
	if err != nil {
		return "", fmt.Errorf("generateJSON failed: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}
