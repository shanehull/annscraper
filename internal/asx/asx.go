/*
Package asx provides utilities for scraping, processing and annotating ASX announcements.
*/
package asx

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shanehull/annscraper/internal/ai"
	"github.com/shanehull/annscraper/internal/types"

	"golang.org/x/net/html"
)

const (
	asxAnnouncementsTodayURL    = "https://www.asx.com.au/asx/v2/statistics/todayAnns.do"
	asxAnnouncementsPreviousURL = "https://www.asx.com.au/asx/v2/statistics/prevBusDayAnns.do"
	asxAnnouncementsByTickerURL = "https://www.asx.com.au/asx/v2/statistics/announcements.do?by=asxCode&timeframe=D&period=M%d&asxCode=%s"
	asxBaseURL                  = "https://www.asx.com.au"
	asxTermsAction              = "/asx/v2/statistics/announcementTerms.do"
	pdfProcessingTimeout        = 60 * time.Second
)

var client = &http.Client{
	Timeout: 60 * time.Second,
}

type cellProcessorFunc func(n *html.Node, tdIndex int, ann *types.Announcement)

func ScrapeDailyFeed(previousDay bool, filterPriceSensitive bool) ([]types.Announcement, error) {
	var url string

	if previousDay {
		url = asxAnnouncementsPreviousURL
	} else {
		url = asxAnnouncementsTodayURL
	}

	return scrapePage(url, filterPriceSensitive)
}

func ScrapeHistoric(ticker string, months int, filterPriceSensitive bool) ([]types.Announcement, error) {
	url := fmt.Sprintf(asxAnnouncementsByTickerURL, months, ticker)

	announcements, err := scrapeHistoricPage(url, ticker, filterPriceSensitive)
	if err != nil {
		return nil, fmt.Errorf("failed to scrape historic announcements for %s: %w", ticker, err)
	}

	return announcements, nil
}

func ProcessAnnouncements(announcements []types.Announcement, keywords []string, tickers []string, filterFn func(types.Announcement, []string, bool) []string, geminiAPIKey string, modelName string) []types.AnnotatedMatch {
	var wg sync.WaitGroup
	matchChan := make(chan types.AnnotatedMatch)

	sem := make(chan struct{}, 10) // Concurrency limit
	total := len(announcements)
	processedCount := 0
	var processedMutex sync.Mutex

	for _, ann := range announcements {
		wg.Add(1)
		sem <- struct{}{}

		go func(a types.Announcement) {
			defer wg.Done()
			defer func() { <-sem }()

			processedMutex.Lock()
			processedCount++
			log.Printf("Processing... %d/%d (%s) ", processedCount, total, a.Ticker)
			processedMutex.Unlock()

			match, analysis, err := filterAndAnnotate(a, keywords, tickers, filterFn, geminiAPIKey, modelName)
			if err != nil {
				log.Printf("Error processing %s (%s): %v", a.Ticker, a.Title, err)
				return
			}

			if match != nil {
				matchChan <- types.AnnotatedMatch{
					Match:    *match,
					Analysis: analysis,
				}
			}
		}(ann)
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

func filterAndAnnotate(ann types.Announcement, keywords []string, tickers []string, filterFn func(types.Announcement, []string, bool) []string, geminiAPIKey string, modelName string) (*types.Match, *ai.AIAnalysis, error) {
	tickerMatch := isTickerMatch(ann.Ticker, tickers)

	pdfURL, err := getPDFURLFromDoURL(ann.PDFURL)
	if err != nil {
		return nil, nil, fmt.Errorf("initial PDF link resolution failed: %w", err)
	}
	ann.PDFURL = pdfURL

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

	analysis := runAIAnalysis(ann.Ticker, text, geminiAPIKey, modelName)

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

func runAIAnalysis(ticker, text, geminiAPIKey, modelName string) *ai.AIAnalysis {
	if geminiAPIKey == "" {
		return nil
	}

	historicAnnouncements, err := ScrapeHistoric(ticker, 6, true)
	if err != nil {
		log.Printf("Warning: Failed to scrape historic announcements for %s: %v", ticker, err)
	}

	historicList := make([]string, 0, len(historicAnnouncements))
	for _, a := range historicAnnouncements {
		historicList = append(historicList, fmt.Sprintf("%s - %s", a.Title, a.PDFURL))
	}

	analysis, err := ai.GenerateSummary(ticker, text, historicList, geminiAPIKey, modelName)
	if err != nil {
		log.Printf("Warning: AI summary failed for %s: %v", ticker, err)
		return nil
	}
	return analysis
}

func scrapePage(url string, filterPriceSensitive bool) ([]types.Announcement, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL %s: %w", url, err)
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Printf("Warning: Failed to close response body for %s: %v", url, err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-OK status code %d from %s", resp.StatusCode, url)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML from %s: %w", url, err)
	}

	processTableCell := func(n *html.Node, tdIndex int, ann *types.Announcement) {
		switch tdIndex {
		case 1: // Ticker
			ann.Ticker = strings.TrimSpace(extractText(n))
		case 2: // Date and Time
			text := strings.TrimSpace(extractText(n))
			cleanedText := regexp.MustCompile(`[\n\t\r\s\xA0]+`).ReplaceAllString(text, " ")
			cleanedText = strings.TrimSpace(cleanedText)
			upperText := strings.ToUpper(cleanedText)

			t, err := time.Parse("02/01/2006 3:04 PM", upperText)
			if err == nil {
				ann.DateTime = t
			} else {
				log.Printf("Warning: Failed to parse date string '%s': %v", cleanedText, err)
			}
		case 3: // Price Sensitive Marker
			for _, attr := range n.Attr {
				if attr.Key == "class" && strings.Contains(attr.Val, "pricesens") {
					ann.IsPriceSensitive = true
					break
				}
			}
		case 4: // Announcement Title and PDF Link
			var aTag *html.Node
			var findATag func(*html.Node)

			findATag = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					aTag = n
					return
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if aTag != nil {
						return
					}
					findATag(c)
				}
			}
			findATag(n)

			if aTag != nil {
				for _, attr := range aTag.Attr {
					if attr.Key == "href" {
						ann.PDFURL = asxBaseURL + strings.TrimSpace(attr.Val)
						break
					}
				}

				var titleBuilder strings.Builder
				for c := aTag.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						text := strings.TrimSpace(c.Data)
						if text != "" {
							titleBuilder.WriteString(text)
						}
					} else if c.Type == html.ElementNode && c.Data == "br" {
						break
					}
				}
				ann.Title = strings.TrimSpace(titleBuilder.String())
			}
		}
	}

	return traverseAndCollect(doc, filterPriceSensitive, processTableCell)
}

func scrapeHistoricPage(url string, tickerCode string, filterPriceSensitive bool) ([]types.Announcement, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL %s: %w", url, err)
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Printf("Warning: Failed to close response body for %s: %v", url, err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-OK status code %d from %s", resp.StatusCode, url)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML from %s: %w", url, err)
	}

	processTickerTableCell := func(n *html.Node, tdIndex int, ann *types.Announcement) {
		// Ticker is set from the function arg, as it's not in the table
		ann.Ticker = tickerCode

		switch tdIndex {
		case 1: // Date and Time
			text := strings.TrimSpace(extractText(n))
			cleanedText := regexp.MustCompile(`[\n\t\r\s\xA0]+`).ReplaceAllString(text, " ")
			cleanedText = strings.TrimSpace(cleanedText)
			upperText := strings.ToUpper(cleanedText)

			t, err := time.Parse("02/01/2006 3:04 PM", upperText)
			if err == nil {
				ann.DateTime = t
			} else {
				log.Printf("Warning: Failed to parse date string '%s': %v", cleanedText, err)
			}
		case 2: // Price Sensitive Marker (Now tdIndex 2)
			for _, attr := range n.Attr {
				if attr.Key == "class" && strings.Contains(attr.Val, "pricesens") {
					ann.IsPriceSensitive = true
					break
				}
			}

		case 3: // Announcement Title and PDF Link
			var aTag *html.Node
			var findATag func(*html.Node)
			findATag = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					aTag = n
					return
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if aTag != nil {
						return
					}
					findATag(c)
				}
			}
			findATag(n)

			if aTag != nil {
				for _, attr := range aTag.Attr {
					if attr.Key == "href" {
						ann.PDFURL = asxBaseURL + strings.TrimSpace(attr.Val)
						break
					}
				}
				var titleBuilder strings.Builder
				for c := aTag.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						text := strings.TrimSpace(c.Data)
						if text != "" {
							titleBuilder.WriteString(text)
						}
					} else if c.Type == html.ElementNode && c.Data == "br" {
						break
					}
				}
				ann.Title = strings.TrimSpace(titleBuilder.String())
			}
		}
	}

	return traverseAndCollect(doc, filterPriceSensitive, processTickerTableCell)
}

func extractText(n *html.Node) string {
	var extract func(*html.Node) string

	extract = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var sb strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			sb.WriteString(extract(c))
		}
		return sb.String()
	}

	return extract(n)
}

func traverseAndCollect(doc *html.Node, filterPriceSensitive bool, processor cellProcessorFunc) ([]types.Announcement, error) {
	var announcements []types.Announcement
	var f func(*html.Node)
	var inTableBody bool
	var currentAnn types.Announcement

	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tbody" {
			inTableBody = true
		}

		if inTableBody {
			if n.Type == html.ElementNode && n.Data == "tr" {
				currentAnn = types.Announcement{}
				tdCount := 0
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "td" {
						tdCount++
						processor(c, tdCount, &currentAnn)
					}
				}

				if currentAnn.PDFURL != "" {
					if filterPriceSensitive && !currentAnn.IsPriceSensitive {
						return
					}
					announcements = append(announcements, currentAnn)
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}

	f(doc)

	return announcements, nil
}
