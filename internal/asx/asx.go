package asx

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shanehull/annscraper/internal/types"

	"golang.org/x/net/html"
)

const asxAnnouncementsTodayURL = "https://www.asx.com.au/asx/v2/statistics/todayAnns.do"
const asxAnnouncementsPrevURL = "https://www.asx.com.au/asx/v2/statistics/prevBusDayAnns.do"
const asxBaseURL = "https://www.asx.com.au"
const asxTermsAction = "/asx/v2/statistics/announcementTerms.do"
const pdfProcessingTimeout = 60 * time.Second

var client = &http.Client{
	Timeout: 60 * time.Second,
}

func ScrapeAnnouncements(filterPriceSensitive bool, previous bool) ([]types.Announcement, error) {
	asxAnnouncementsURL := asxAnnouncementsTodayURL
	if previous {
		asxAnnouncementsURL = asxAnnouncementsPrevURL
	}

	resp, err := client.Get(asxAnnouncementsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-OK status code: %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var announcements []types.Announcement
	var f func(*html.Node)
	var inTableBody bool

	var processTableCell func(*html.Node, int, *types.Announcement)
	processTableCell = func(n *html.Node, tdIndex int, ann *types.Announcement) {
		var extractText func(*html.Node) string
		extractText = func(n *html.Node) string {
			if n.Type == html.TextNode {
				return n.Data
			}
			var sb strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				sb.WriteString(extractText(c))
			}
			return sb.String()
		}

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

	var currentAnn types.Announcement
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tbody" {
			inTableBody = true
		}

		if inTableBody {
			if n.Type == html.ElementNode && n.Data == "tr" {
				currentAnn = types.Announcement{} // Use types.Announcement
				tdCount := 0
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "td" {
						tdCount++
						processTableCell(c, tdCount, &currentAnn)
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

func ProcessAnnouncements(announcements []types.Announcement, keywords []string, filterFn func(types.Announcement, []string) []string) []types.Match {
	var wg sync.WaitGroup
	matchChan := make(chan types.Match) // Use types.Match
	sem := make(chan struct{}, 10)      // Concurrency limit
	total := len(announcements)
	processedCount := 0
	var processedMutex sync.Mutex

	for _, ann := range announcements {
		wg.Add(1)
		sem <- struct{}{} // Acquire token

		go func(a types.Announcement) {
			defer wg.Done()
			defer func() { <-sem }() // Release token

			processedMutex.Lock()
			processedCount++
			fmt.Printf("\rProcessing... %d/%d (%s) ", processedCount, total, a.Ticker)
			processedMutex.Unlock()

			match, err := searchAnnouncement(a, keywords, filterFn)
			if err != nil {
				log.Printf("Error processing %s (%s): %v", a.Ticker, a.Title, err)
				return
			}

			if match != nil {
				matchChan <- *match
			}
		}(ann)
	}

	go func() {
		wg.Wait()
		close(matchChan)
	}()

	var matches []types.Match
	for match := range matchChan {
		matches = append(matches, match)
	}
	fmt.Printf("\nDone processing.\n")
	return matches
}

func searchAnnouncement(ann types.Announcement, keywords []string, filterFn func(types.Announcement, []string) []string) (*types.Match, error) {
	var foundKeywords []string
	lowerTitle := strings.ToLower(ann.Title)

	for _, keyword := range keywords {
		if strings.Contains(lowerTitle, keyword) {
			foundKeywords = append(foundKeywords, keyword)
		}
	}

	text, err := extractTextFromPDF(ann.PDFURL)
	if err != nil {
		return nil, fmt.Errorf("PDF text extraction failed: %w", err)
	}

	lowerText := strings.ToLower(text)

	for _, keyword := range keywords {
		isTitleMatch := false
		for _, fk := range foundKeywords {
			if fk == keyword {
				isTitleMatch = true
				break
			}
		}

		if !isTitleMatch && strings.Contains(lowerText, keyword) {
			foundKeywords = append(foundKeywords, keyword)
		}
	}

	if len(foundKeywords) == 0 {
		return nil, nil
	}

	newKeywords := filterFn(ann, foundKeywords)

	if len(newKeywords) == 0 {
		return nil, nil
	}

	contextKeyword := newKeywords[0]
	contextSnippet := ""

	if strings.Contains(lowerTitle, contextKeyword) {
		contextSnippet = ann.Title + " (Match found in title)"
	} else {
		contextSnippet = getSnippet(text, contextKeyword)
	}

	return &types.Match{
		Announcement:  ann,
		KeywordsFound: newKeywords,
		Context:       contextSnippet,
	}, nil
}
