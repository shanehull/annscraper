/*
Package asx provides utilities for scraping, processing and annotating ASX announcements.
*/
package asx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/shanehull/annscraper/internal/ai"
	"github.com/shanehull/annscraper/internal/types"
)

const (
	markitAnnouncementsURL = "https://asx.api.markitdigital.com/asx-research/1.0/markets/announcements"
	markitPDFBaseURL       = "https://cdn-api.markitdigital.com/apiman-gateway/ASX/asx-research/1.0/file"
	pdfProcessingTimeout   = 120 * time.Second // 2 minutes for PDF text extraction
)

var client = &http.Client{
	Timeout: 180 * time.Second, // 3 minutes for large PDF downloads
}

type markitAnnouncementsResponse struct {
	Data struct {
		Items []struct {
			Companies []struct {
				SymbolDisplay string `json:"symbolDisplay"`
			} `json:"companies"`
			Date        string `json:"date"`
			DocumentKey string `json:"documentKey"`
			Headline    string `json:"headline"`
			Symbol      string `json:"symbol"`
		} `json:"items"`
	} `json:"data"`
}

type FetchParams struct {
	Date               string
	PriceSensitiveOnly bool
	MaxResults         int // 0 = unlimited
}

func FetchAnnouncements(params FetchParams) ([]types.Announcement, error) {
	var allAnnouncements []types.Announcement
	pageSize := 100
	page := 0
	var targetDate time.Time

	// Parse target date if provided
	if params.Date != "" {
		var err error
		targetDate, err = time.Parse("2006-01-02", params.Date)
		if err != nil {
			return nil, fmt.Errorf("invalid date format: %s (expected YYYY-MM-DD)", params.Date)
		}
	}

	for {
		var url string
		if params.Date != "" {
			url = fmt.Sprintf("%s?summaryCountsDate=%s&page=%d&itemsPerPage=%d&priceSensitiveOnly=%v",
				markitAnnouncementsURL, params.Date, page, pageSize, params.PriceSensitiveOnly)
		} else {
			url = fmt.Sprintf("%s?page=%d&itemsPerPage=%d&priceSensitiveOnly=%v",
				markitAnnouncementsURL, page, pageSize, params.PriceSensitiveOnly)
		}

		announcements, hasMore, err := fetchAnnouncements(url, targetDate)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch announcements page %d: %w", page, err)
		}

		allAnnouncements = append(allAnnouncements, announcements...)

		if !hasMore || len(announcements) < pageSize {
			break
		}

		if params.MaxResults > 0 && len(allAnnouncements) >= params.MaxResults {
			allAnnouncements = allAnnouncements[:params.MaxResults]
			break
		}

		page++
	}

	return allAnnouncements, nil
}



func ProcessAnnouncements(ctx context.Context, announcements []types.Announcement, keywords []string, tickers []string, filterFn func(types.Announcement, []string, bool) []string, geminiAPIKey string, modelName string) []types.AnnotatedMatch {
	var wg sync.WaitGroup
	matchChan := make(chan types.AnnotatedMatch)

	sem := make(chan struct{}, 10) // Concurrency limit
	total := len(announcements)
	processedCount := 0
	var processedMutex sync.Mutex

	for _, ann := range announcements {
		sem <- struct{}{}

		wg.Go(func() {
			defer func() { <-sem }()

			processedMutex.Lock()
			processedCount++
			log.Printf("Processing... %d/%d (%s) ", processedCount, total, ann.Ticker)
			processedMutex.Unlock()

			match, analysis, err := filterAndAnnotate(ctx, ann, keywords, tickers, filterFn, geminiAPIKey, modelName)
			if err != nil {
				log.Printf("Error processing %s (%s): %v", ann.Ticker, ann.Title, err)
				return
			}

			if match != nil {
				matchChan <- types.AnnotatedMatch{
					Match:    *match,
					Analysis: analysis,
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(matchChan)
	}()

	var annotatedMatches []types.AnnotatedMatch
	for match := range matchChan {
		annotatedMatches = append(annotatedMatches, match)
	}

	log.Printf("Done processing")

	return annotatedMatches
}

func filterAndAnnotate(ctx context.Context, ann types.Announcement, keywords []string, tickers []string, filterFn func(types.Announcement, []string, bool) []string, geminiAPIKey string, modelName string) (*types.Match, *ai.AIAnalysis, error) {
	tickerMatch := isTickerMatch(ann.Ticker, tickers)

	text, err := extractTextFromPDF(ann.PDFURL)
	if err != nil {
		return nil, nil, fmt.Errorf("PDF text extraction failed: %w", err)
	}

	foundKeywords := findKeywords(ann.Title, text, keywords)

	if len(foundKeywords) == 0 && !tickerMatch {
		return nil, nil, nil
	}

	newKeywords := applyHistoryFilter(ann, foundKeywords, tickerMatch, filterFn)
	if len(newKeywords) == 0 {
		return nil, nil, nil
	}

	finalKeywords, isPlaceholderMatch := normalizePlaceholder(newKeywords)
	contextSnippet := buildContextSnippet(ann, text, finalKeywords, isPlaceholderMatch)

	match := &types.Match{
		Announcement:  ann,
		KeywordsFound: finalKeywords,
		TickerMatched: tickerMatch,
		Context:       contextSnippet,
	}

	analysis := runAIAnalysis(ctx, ann.Ticker, text, geminiAPIKey, modelName)

	return match, analysis, nil
}

func isTickerMatch(ticker string, tickers []string) bool {
	if len(tickers) == 0 {
		return false
	}
	tickerMap := make(map[string]struct{}, len(tickers))
	for _, t := range tickers {
		tickerMap[t] = struct{}{}
	}
	_, match := tickerMap[ticker]
	return match
}

func findKeywords(title, text string, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}

	var found []string
	lowerTitle := strings.ToLower(title)
	lowerText := strings.ToLower(text)

	for _, kw := range keywords {
		if strings.Contains(lowerTitle, kw) {
			found = append(found, kw)
		} else if strings.Contains(lowerText, kw) {
			found = append(found, kw)
		}
	}
	return found
}

func applyHistoryFilter(ann types.Announcement, foundKeywords []string, tickerMatch bool, filterFn func(types.Announcement, []string, bool) []string) []string {
	historyKeywords := foundKeywords
	if tickerMatch && len(historyKeywords) == 0 {
		historyKeywords = []string{types.TickerMatchPlaceholder}
	}
	return filterFn(ann, historyKeywords, tickerMatch)
}

func normalizePlaceholder(keywords []string) (finalKeywords []string, isPlaceholder bool) {
	if len(keywords) == 1 && keywords[0] == types.TickerMatchPlaceholder {
		return nil, true
	}
	return keywords, false
}

func buildContextSnippet(ann types.Announcement, text string, keywords []string, isPlaceholderMatch bool) string {
	if len(keywords) > 0 {
		keyword := keywords[0]
		if strings.Contains(strings.ToLower(ann.Title), keyword) {
			return ann.Title + " (Match found in title)"
		}
		return getSnippet(text, keyword)
	}
	if isPlaceholderMatch {
		return fmt.Sprintf("Match found based on ticker %s only.", ann.Ticker)
	}
	return ""
}

func runAIAnalysis(ctx context.Context, ticker, text, geminiAPIKey, modelName string) *ai.AIAnalysis {
	if geminiAPIKey == "" {
		return nil
	}

	historicAnnouncements, err := FetchAnnouncements(FetchParams{
		PriceSensitiveOnly: true,
		MaxResults:         100,
	})
	if err != nil {
		log.Printf("Warning: Failed to scrape historic announcements for %s: %v", ticker, err)
	}

	// Filter by ticker
	var historicList []string
	for _, a := range historicAnnouncements {
		if a.Ticker == ticker {
			historicList = append(historicList, fmt.Sprintf("%s - %s", a.Title, a.PDFURL))
		}
	}

	analysis, err := ai.GenerateSummary(ctx, ticker, text, historicList[1:], geminiAPIKey, modelName)
	if err != nil {
		log.Printf("Warning: AI summary failed for %s: %v", ticker, err)
		return nil
	}
	return analysis
}

func fetchAnnouncements(url string, targetDate time.Time) ([]types.Announcement, bool, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch URL %s: %w", url, err)
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Printf("Warning: Failed to close response body for %s: %v", url, err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("received non-OK status code %d from %s", resp.StatusCode, url)
	}

	var respData markitAnnouncementsResponse
	if err = json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return nil, false, fmt.Errorf("failed to parse JSON from %s: %w", url, err)
	}

	var announcements []types.Announcement
	for _, item := range respData.Data.Items {
		if item.DocumentKey == "" {
			continue
		}

		// Parse date
		var itemDate time.Time
		if t, err := time.Parse(time.RFC3339, item.Date); err == nil {
			itemDate = t
		} else {
			log.Printf("Warning: Failed to parse date string '%s': %v", item.Date, err)
			continue
		}

		// Filter by target date if provided (compare date part only)
		if !targetDate.IsZero() {
			if itemDate.Year() != targetDate.Year() || itemDate.Month() != targetDate.Month() || itemDate.Day() != targetDate.Day() {
				continue
			}
		}

		ann := types.Announcement{
			Ticker:           item.Symbol,
			Title:            item.Headline,
			IsPriceSensitive: true, // Markit API indicates price sensitive by filtering
			DateTime:         itemDate,
			PDFURL:           fmt.Sprintf("%s/%s", markitPDFBaseURL, item.DocumentKey),
		}

		announcements = append(announcements, ann)
	}

	// Check if there are more results
	hasMore := len(respData.Data.Items) > 0
	return announcements, hasMore, nil
}

func getSnippet(fullText string, keyword string) string {
	const contextSize = 50

	lowerText := strings.ToLower(fullText)
	lowerKeyword := strings.ToLower(keyword)

	index := strings.Index(lowerText, lowerKeyword)
	if index == -1 {
		return ""
	}

	start := max(index-contextSize, 0)
	end := min(index+len(lowerKeyword)+contextSize, len(fullText))

	snippet := fullText[start:end]

	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(fullText) {
		snippet = snippet + " ..."
	}

	return strings.ReplaceAll(snippet, "\n", " ")
}

func extractTextFromPDF(pdfURL string) (string, error) {
	resp, err := client.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed initial GET to %s: %w", pdfURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("Warning: failed to close response body for %s: %v", pdfURL, cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download PDF: received status code %d from %s", resp.StatusCode, pdfURL)
	}

	pdfBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF response body: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), pdfProcessingTimeout)
	defer cancel()

	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		tmpFile, err := os.CreateTemp("", "asx_pdf_*.pdf")
		if err != nil {
			errChan <- fmt.Errorf("failed to create temporary file: %w", err)
			return
		}
		tmpFileName := tmpFile.Name()
		err = tmpFile.Close()
		if err != nil {
			errChan <- fmt.Errorf("failed to close temporary file: %w", err)
		}
		defer func() {
			if rerr := os.Remove(tmpFileName); rerr != nil {
				log.Printf("Warning: failed to remove temp file %s: %v", tmpFileName, rerr)
			}
		}()

		if err := os.WriteFile(tmpFileName, pdfBytes, 0o644); err != nil {
			errChan <- fmt.Errorf("failed to write PDF bytes to temp file: %w", err)
			return
		}

		cmd := exec.CommandContext(ctx, "pdftotext", "-raw", tmpFileName, "-")

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			cmdErr := fmt.Errorf("pdftotext failed: %v. Stderr: %s", err, strings.TrimSpace(stderr.String()))
			if strings.Contains(cmdErr.Error(), "not found") {
				errChan <- fmt.Errorf("pdftotext binary not found. Please ensure poppler-utils is installed. Error: %s", strings.TrimSpace(stderr.String()))
			} else {
				errChan <- cmdErr
			}
			return
		}

		text := out.String()

		if strings.TrimSpace(text) == "" {
			errChan <- fmt.Errorf("pdftotext extracted empty text string. File may be image-based or protected")
			return
		}

		resultChan <- text
	}()

	select {
	case text := <-resultChan:
		return text, nil
	case err := <-errChan:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("PDF text extraction timed out after %s", pdfProcessingTimeout)
	}
}
