/*
Package ai provides functionality to interact with the Gemini AI API and provide
financial analysis of ASX announcements.
*/
package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

type CatalystObservation struct {
	Category string `json:"category"`
	Details  string `json:"details"`
}

type AIAnalysis struct {
	Summary            []string              `json:"summary"`
	PotentialCatalysts []CatalystObservation `json:"potential_catalysts"`
}

func GenerateSummary(ctx context.Context, ticker string, text string, historicAnnouncementsList []string, apiKey string, modelName string) (*AIAnalysis, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini API key is required")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	contents := genai.Text(
		buildUserPrompt(text, historicAnnouncementsList),
	)

	systemContent := &genai.Content{
		Parts: []*genai.Part{
			{Text: systemInstruction},
		},
	}

	tools := []*genai.Tool{
		{
			URLContext:   &genai.URLContext{},
			GoogleSearch: &genai.GoogleSearch{},
		},
	}

	resp, err := client.Models.GenerateContent(ctx, modelName, contents, &genai.GenerateContentConfig{
		SystemInstruction: systemContent,
		ResponseMIMEType:  "application/json",
		ResponseSchema:    getResponseSchema(),
		Tools:             tools,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini API call failed: %w", err)
	}

	respText := resp.Text()

	var analysis AIAnalysis
	if err := json.Unmarshal([]byte(respText), &analysis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gemini JSON response: %w. Raw text: %s", err, respText)
	}

	return &analysis, nil
}

func getResponseSchema() *genai.Schema {
	catalystSchema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"category": {Type: genai.TypeString, Description: "One of the defined catalyst categories."},
			"details":  {Type: genai.TypeString, Description: "Specific financial data or transaction terms."},
		},
		Required: []string{"category", "details"},
	}

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary": {
				Type:        genai.TypeArray,
				Items:       &genai.Schema{Type: genai.TypeString},
				Description: "A list of 3-5 concise bullet points summarizing the document.",
			},
			"potential_catalysts": {
				Type:        genai.TypeArray,
				Items:       catalystSchema,
				Description: "A list of specific, actionable observations.",
			},
		},
		Required: []string{"summary", "potential_catalysts"},
	}
}
