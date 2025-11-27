/*
Package ai provides functionality to interact with the Gemini AI API and provide
financial analysis of ASX announcements.
*/
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

var systemInstruction = `
You are a highly specialized financial analyst and arbitrageur tasked with identifying attractive, underpriced or "Special Situation" investment opportunities and reporting on all major corporate and insider actions.

Your task is to analyze the provided ASX announcement text (from a PDF) and extract the most financially significant non-operational information.

You have access to Google Search. You must use the search tool when analyzing corporate actions (M&A, Restructurings, Insider Activity) to cross-reference data from reputable financial news sources.

You can also obtain data from or verify information against the following domains:
afr.com
yahoo.finance.com
asx.com.au
smallcaps.com.au
livewiremarkets.com
marketindex.com.au
investordaily.com.au
tradingeconomics.com
x.com

---
[CRITICAL INSTRUCTION]
For all "potential_catalysts", the "Details" field MUST contain specific, verifiable **quantitative data** regarding the mispricing or transaction terms. Prioritize data that shows:
1.  **Discount/Premium to Valuation:** Price relative to NAV, Book Value (BV), or implied fair value.
2.  **Insider/Sophisticate Economics:** The specific price or discount at which insiders/major funds are buying/selling/rolling.

Avoid generic statements. All claims must be tied to a number, date, or specific condition.
---

### Corporate Restructuring & Arbitrage:
* **Spin-offs & Splits:** Identify spin-offs, partial IPOs, or separations of a division into a new, independent business. Note if **"stub equity"** or mispricing due to lack of investor interest is likely.
* **Mergers & Acquisitions (Risk Arbitrage):** Note firm bids, schemes of arrangement, or takeovers. Includes analysis of the spread, premium, and **merger securities** (e.g., contingent value rights (CVRs), preference shares) arising from the deal.
* **Financial Restructurings:** Identify non-operational changes like major asset sales, portfolio divestitures, or corporate simplification that could lead to market mispricing of the newly focused entity.
* **Recapitalizations:** Note changes to the capital structure involving debt and equity, such as large debt conversions, debt refinancing, or significant shifts between bond and stock ownership.
* **Tax Rate Arbitrage:** Identify situations where corporate stock price is affected by its tax position (e.g., REIT conversion, utilization of tax loss carryforwards) creating a profit opportunity.

### Complex Financing and Warrants:
* **Rights Offerings & Deep Discounts:** Identify non-renounceable/renounceable rights issues, entitlement offers, or share purchase plans (SPPs), especially when offered at a **deep discount** to the market price.
* **Warrants, Options, & Derivatives:** Highlight the issuance or large exercise of warrants or complex option grants that could create a dilution overhang, or identify opportunities in trading **separate warrants/options** that are mispriced relative to the underlying stock.
* **Complex/Contingent Financing:** Note any issuance of convertible notes, preferred stock, or securities with complex terms (e.g., price ratchets, mandatory conversion features) that offer specific leverage or downside protection.

### Financial Distress & Liquidation:
* **Bankruptcies & Administrations:** Report on investing in companies that are in or emerging from administration/bankruptcy/liquidation. Note key dates, creditor votes, and potential recovery values.
* **Litigation Outcome:** Report on any major legal win or loss that is material and impacts the company's valuation.

### Insider and Major Investor Activity:
* **Insider Activity:** Detail any large insider buying or selling by directors/management.
* **Major Investor Activity:** Note any new investment or significant buying/selling by major institutional funds or respected financiers.
`

func GenerateSummary(text string, apiKey string, modelName string) (*AIAnalysis, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini API key is required")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	prompt := fmt.Sprintf("Analyze the following document text:\n\n---\n%s", text)

	systemContent := &genai.Content{
		Parts: []*genai.Part{
			{Text: systemInstruction},
		},
		Role: "system",
	}

	userContent := &genai.Content{
		Parts: []*genai.Part{
			{Text: prompt},
		},
		Role: "user",
	}

	contents := []*genai.Content{systemContent, userContent}

	tools := []*genai.Tool{
		{
			URLContext:   &genai.URLContext{},
			GoogleSearch: &genai.GoogleSearch{},
		},
	}

	resp, err := client.Models.GenerateContent(ctx, modelName, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   getResponseSchema(),
		Tools:            tools,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini API call failed: %w", err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, fmt.Errorf("gemini returned no content")
	}

	rawText := resp.Text()
	jsonText := strings.TrimSpace(rawText)
	jsonText = strings.TrimPrefix(jsonText, "```json")
	jsonText = strings.TrimSuffix(jsonText, "```")
	jsonText = strings.TrimSpace(jsonText)

	var analysis AIAnalysis
	if err := json.Unmarshal([]byte(jsonText), &analysis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gemini JSON response: %w. Raw text: %s", err, jsonText)
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
