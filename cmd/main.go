package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"flag"

	"golang.org/x/net/html"
	gomail "gopkg.in/mail.v2"
)

const asxAnnouncementsURL = "https://www.asx.com.au/asx/v2/statistics/todayAnns.do"
const asxBaseURL = "https://www.asx.com.au"
const asxTermsAction = "/asx/v2/statistics/announcementTerms.do"
const pdfProcessingTimeout = 60 * time.Second
const historyFileName = "asx_report_history.json"
const historyDirName = "asx_scraper"

var client = &http.Client{
	Timeout: 60 * time.Second,
}

type Announcement struct {
	Ticker           string
	DateTime         time.Time
	Title            string
	PDFURL           string
	IsPriceSensitive bool
}

type Match struct {
	Announcement
	KeywordsFound []string
	Context       string
}

type History struct {
	ReportDate      string
	ReportedMatches map[string]map[string]bool
}

var processedCount int
var history History
var historyMutex sync.Mutex
var historyFilePath string

type EmailConfig struct {
	SMTPServer string
	SMTPPort   int
	SMTPUser   string
	SMTPPass   string
	FromEmail  string
	ToEmail    string
	Enabled    bool
}

func loadHistory() {
	historyMutex.Lock()
	defer historyMutex.Unlock()

	today := time.Now().Format("2006-01-02")
	history = History{
		ReportDate:      today,
		ReportedMatches: make(map[string]map[string]bool),
	}

	data, err := os.ReadFile(historyFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("History file %s not found. Starting fresh report.", historyFilePath)
			return
		}
		log.Printf("Error reading history file (%s): %v. Starting fresh report.", historyFilePath, err)
		return
	}

	var loadedHistory History
	if err := json.Unmarshal(data, &loadedHistory); err != nil {
		log.Printf("Error unmarshalling history JSON: %v. Starting fresh report.", err)
		return
	}

	if loadedHistory.ReportDate == today {
		history = loadedHistory
		log.Printf("Loaded %d reported matches for today (%s).", len(history.ReportedMatches), today)
	} else {
		log.Printf("History is from %s. Starting new report history for today (%s).", loadedHistory.ReportDate, today)
	}
}

func saveHistory() {
	historyMutex.Lock()
	defer historyMutex.Unlock()

	history.ReportDate = time.Now().Format("2006-01-02")

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		log.Printf("Error marshalling history for save: %v", err)
		return
	}

	if err := os.WriteFile(historyFilePath, data, 0644); err != nil {
		log.Printf("Error writing history file %s: %v", historyFilePath, err)
	} else {
		log.Printf("Successfully saved report history to %s.", historyFilePath)
	}
}

func recordMatch(ann Announcement, newKeywords []string) {
	historyMutex.Lock()
	defer historyMutex.Unlock()

	key := ann.Ticker + "|" + ann.Title

	if history.ReportedMatches[key] == nil {
		history.ReportedMatches[key] = make(map[string]bool)
	}

	for _, kw := range newKeywords {
		history.ReportedMatches[key][kw] = true
	}
}

func filterNewMatches(ann Announcement, foundKeywords []string) []string {
	historyMutex.Lock()
	defer historyMutex.Unlock()

	key := ann.Ticker + "|" + ann.Title

	reportedKws, exists := history.ReportedMatches[key]
	if !exists {
		return foundKeywords
	}

	var newKeywords []string
	for _, kw := range foundKeywords {
		if !reportedKws[kw] {
			newKeywords = append(newKeywords, kw)
		}
	}
	return newKeywords
}

func main() {
	keywordStr := flag.String("keywords", "", "Comma-separated list of keywords or exact phrases (e.g., 'dividend, Rick Rule, profit')")

	filterPriceSensitive := flag.Bool("price-sensitive", false, "(-s) Process ONLY price sensitive announcements (default: false). Set to false to include all.")

	smtpServer := flag.String("smtp-server", "", "SMTP server address (e.g., smtp.gmail.com)")
	smtpPort := flag.Int("smtp-port", 587, "SMTP server port (e.g., 587)")
	smtpUser := flag.String("smtp-user", "", "SMTP username (email address)")
	smtpPass := flag.String("smtp-pass", "", "SMTP password or App Password")
	toEmail := flag.String("to-email", "", "Recipient email address")
	fromEmail := flag.String("from-email", "", "Sender email address (must match user/auth)")

	flag.Parse()

	if *keywordStr == "" {
		fmt.Println("Error: Keywords are required.")
		fmt.Println("Usage: go run asx_scraper.go -keywords 'keyword1' [-s=false] --smtp-server=... --to-email=...")
		os.Exit(1)
	}

	emailConfig := EmailConfig{
		SMTPServer: *smtpServer,
		SMTPPort:   *smtpPort,
		SMTPUser:   *smtpUser,
		SMTPPass:   *smtpPass,
		ToEmail:    *toEmail,
		FromEmail:  *fromEmail,
		Enabled:    (*smtpServer != "" && *smtpUser != "" && *smtpPass != "" && *toEmail != "" && *fromEmail != ""),
	}

	// History Setup
	historyDir := filepath.Join(os.TempDir(), historyDirName)
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		log.Fatalf("Failed to create temporary history directory %s: %v", historyDir, err)
	}
	historyFilePath = filepath.Join(historyDir, historyFileName)

	keywords := parseKeywords(*keywordStr)

	fmt.Printf("Starting ASX Scraper. Searching for keywords/phrases: %s\n\n", strings.Join(keywords, ", "))

	loadHistory()

	announcements, err := scrapeAnnouncements(*filterPriceSensitive)
	if err != nil {
		log.Fatalf("Fatal error during scraping: %v", err)
	}

	totalAnns := len(announcements)
	if totalAnns == 0 {
		fmt.Println("No announcements found today or scraping failed.")
		return
	}
	fmt.Printf("Found %d total announcements (price-sensitive: %t). Starting PDF download and search...\n", totalAnns, *filterPriceSensitive)

	matches := processAnnouncements(announcements, keywords, totalAnns)

	if len(matches) == 0 {
		fmt.Println("\n-------------------------------------------")
		fmt.Println("No new matching keywords found in any announcement today.")
		fmt.Println("-------------------------------------------")
		saveHistory()
		return
	}

	reportMatches(matches)

	if emailConfig.Enabled {
		log.Printf("Emailing matches (SMTP: %s:%d).", emailConfig.SMTPServer, emailConfig.SMTPPort)
		emailMatches(matches, emailConfig)
	}

	saveHistory()
}

func parseKeywords(s string) []string {
	parts := strings.Split(s, ",")
	var keywords []string
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed != "" {
			keywords = append(keywords, trimmed)
		}
	}
	return keywords
}

func scrapeAnnouncements(filterPriceSensitive bool) ([]Announcement, error) {
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

	var announcements []Announcement
	var f func(*html.Node)
	var inTableBody bool
	var currentAnn Announcement

	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tbody" {
			inTableBody = true
		}

		if inTableBody {
			if n.Type == html.ElementNode && n.Data == "tr" {
				currentAnn = Announcement{}
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

func processTableCell(n *html.Node, tdIndex int, ann *Announcement) {
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
	case 1: // Ticker (first column)
		ann.Ticker = strings.TrimSpace(extractText(n))
	case 2: // Date and Time
		text := strings.TrimSpace(extractText(n))

		cleanedText := regexp.MustCompile(`[\n\t\r\s\xA0]+`).ReplaceAllString(text, " ")
		cleanedText = strings.TrimSpace(cleanedText)

		upperText := strings.ToUpper(cleanedText)

		// Expected format: DD/MM/YYYY HH:MM AM/PM
		t, err := time.Parse("02/01/2006 3:04 PM", upperText)
		if err == nil {
			ann.DateTime = t
		} else {
			log.Printf("Warning: Failed to parse date string '%s' (cleaned: '%s') with format '02/01/2006 3:04 PM': %v", text, cleanedText, err)
		}

	case 3: // Price Sensitive Marker (third column)
		// Check for the specific class attribute that indicates price sensitivity
		for _, attr := range n.Attr {
			if attr.Key == "class" && strings.Contains(attr.Val, "pricesens") {
				ann.IsPriceSensitive = true
				break
			}
		}

	case 4: // Announcement Title and PDF Link
		// Find the <a> tag
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
			// Extract PDF URL
			for _, attr := range aTag.Attr {
				if attr.Key == "href" {
					relURL := strings.TrimSpace(attr.Val)
					// Construct the absolute URL
					ann.PDFURL = asxBaseURL + relURL
					break
				}
			}

			// Extract Title (text inside the <a> tag, before the <br> or <img>)
			// The title might contain the file size/page count text, so we clean it up
			// A reliable way is to find the first non-image/non-span child of the <a> tag
			var titleBuilder strings.Builder
			for c := aTag.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					text := strings.TrimSpace(c.Data)
					if text != "" {
						titleBuilder.WriteString(text)
					}
				} else if c.Type == html.ElementNode && c.Data == "br" {
					break // Title ends before the line break
				}
			}
			ann.Title = strings.TrimSpace(titleBuilder.String())
		}
	}
}

func processAnnouncements(announcements []Announcement, keywords []string, total int) []Match {
	var wg sync.WaitGroup
	matchChan := make(chan Match)

	// Concurrency Semaphore limit to balance speed and stability.
	sem := make(chan struct{}, 10)

	processedCount = 0

	for _, ann := range announcements {
		wg.Add(1)
		sem <- struct{}{} // Acquire token

		go func(a Announcement) {
			defer wg.Done()
			defer func() { <-sem }() // Release token

			// Update progress count. Note: Total is now the filtered count.
			processedCount++
			fmt.Printf("\rProcessing... %d/%d (%s) ", processedCount, total, a.Ticker)

			match, err := searchAnnouncement(a, keywords)
			if err != nil {
				log.Printf("Error processing %s (%s): %v", a.Ticker, a.Title, err)
				return
			}

			if match != nil {
				recordMatch(a, match.KeywordsFound)
				matchChan <- *match
			}
		}(ann)
	}

	// Wait for all workers to finish and close the channel
	go func() {
		wg.Wait()
		close(matchChan)
	}()

	var matches []Match
	for match := range matchChan {
		matches = append(matches, match)
	}
	fmt.Printf("\nDone processing.\n")
	return matches
}

func searchAnnouncement(ann Announcement, keywords []string) (*Match, error) {
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
		// Check if keyword was already found in the title
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

	newKeywords := filterNewMatches(ann, foundKeywords)

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

	return &Match{
		Announcement:  ann,
		KeywordsFound: newKeywords,
		Context:       contextSnippet,
	}, nil
}

func getSnippet(fullText string, keyword string) string {
	// Define context size (e.g., 50 characters before and after)
	const contextSize = 50

	// Find the index of the first occurrence (case-insensitive)
	lowerText := strings.ToLower(fullText)
	lowerKeyword := strings.ToLower(keyword)

	index := strings.Index(lowerText, lowerKeyword)
	if index == -1 {
		return ""
	}

	// Determine start of snippet
	start := index - contextSize
	if start < 0 {
		start = 0
	}

	// Determine end of snippet
	end := index + len(lowerKeyword) + contextSize
	if end > len(fullText) {
		end = len(fullText)
	}

	snippet := fullText[start:end]

	// Add ellipses if the snippet doesn't cover the whole text
	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(fullText) {
		snippet = snippet + " ..."
	}

	return strings.ReplaceAll(snippet, "\n", " ") // Clean up newlines for display
}

// extractTextFromPDF handles the T&C bypass, downloads the PDF bytes, and uses the external pdftotext utility.
func extractTextFromPDF(asxTriggerURL string) (string, error) {
	// 1. Initial request to the ASX trigger URL to handle potential T&C form
	resp, err := client.Get(asxTriggerURL)
	if err != nil {
		return "", fmt.Errorf("failed initial GET to %s: %w", asxTriggerURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	bodyString := string(bodyBytes)

	finalPDFURL := asxTriggerURL // Assume success initially

	// Check and bypass the T&C form if present
	if strings.Contains(bodyString, asxTermsAction) {
		// Regex to find the hidden PDF URL in the T&C form
		re := regexp.MustCompile(`name="pdfURL"\s+value="(.*?)"`)
		match := re.FindStringSubmatch(bodyString)

		if len(match) < 2 {
			return "", fmt.Errorf("T&C form detected, but could not find the hidden 'pdfURL' field")
		}

		directPDFURL := match[1]

		// Submit the "Agree and proceed" form via POST to set the session cookie.
		formValues := url.Values{
			"pdfURL":                  {directPDFURL},
			"showAnnouncementPDFForm": {"Agree and proceed"},
		}

		termsURL := asxBaseURL + asxTermsAction
		_, err = client.PostForm(termsURL, formValues)
		if err != nil {
			log.Printf("Warning: T&C POST submission failed or redirected unexpectedly: %v", err)
		}

		finalPDFURL = directPDFURL // Use the direct URL for the final download
	}

	// Download the actual PDF content bytes
	pdfResp, err := client.Get(finalPDFURL)
	if err != nil {
		return "", fmt.Errorf("failed to download final PDF from %s: %w", finalPDFURL, err)
	}
	defer pdfResp.Body.Close()

	if pdfResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download PDF: received status code %d from %s", pdfResp.StatusCode, finalPDFURL)
	}

	pdfBytes, err := io.ReadAll(pdfResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF response body: %w", err)
	}

	// Setup context for execution time limit
	ctx, cancel := context.WithTimeout(context.Background(), pdfProcessingTimeout)
	defer cancel()

	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		// Write PDF bytes to a temporary file
		// os.CreateTemp creates the file in the system's TEMP directory, which is correct and secure.
		tmpFile, err := os.CreateTemp("", "asx_pdf_*.pdf")
		if err != nil {
			errChan <- fmt.Errorf("failed to create temporary file: %w", err)
			return
		}
		tmpFileName := tmpFile.Name()
		tmpFile.Close()

		defer os.Remove(tmpFileName) // Clean up the temp file when finished

		if err := os.WriteFile(tmpFileName, pdfBytes, 0644); err != nil {
			errChan <- fmt.Errorf("failed to write PDF bytes to temp file: %w", err)
			return
		}

		// Execute pdftotext command
		cmd := exec.CommandContext(ctx, "pdftotext", "-raw", tmpFileName, "-")

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			errMsg := fmt.Sprintf("pdftotext failed: %v. Stderr: %s", err, strings.TrimSpace(stderr.String()))
			// Check specifically for "not found" error
			if strings.Contains(errMsg, "no such file or directory") || strings.Contains(errMsg, "not found") {
				errChan <- fmt.Errorf("pdftotext binary not found. Please ensure poppler-utils is installed and 'pdftotext' is in your PATH. Error: %s", strings.TrimSpace(stderr.String()))
			} else {
				errChan <- fmt.Errorf(errMsg)
			}
			return
		}

		text := out.String()

		if strings.TrimSpace(text) == "" {
			// If pdftotext runs but returns no text, it might be an image-only or a protected file
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
		// This happens if the goroutine takes longer than pdfProcessingTimeout
		return "", fmt.Errorf("PDF text extraction timed out after %s", pdfProcessingTimeout)
	}
}

func emailMatches(matches []Match, emailConfig EmailConfig) {
	// Use a WaitGroup to ensure all network operations finish before the program exits
	var wg sync.WaitGroup

	for _, match := range matches {
		wg.Add(1)

		emailBody := fmt.Sprintf("Ticker: %s\nTitle: %s\nPrice Sensitive: %t\nDate: %s\nURL: %s\n\nKeywords: %s\n\nContext Snippet:\n%s",
			match.Ticker,
			match.Title,
			match.IsPriceSensitive,
			match.DateTime.Format("02 Jan 2006 3:04 PM"),
			match.PDFURL,
			strings.Join(match.KeywordsFound, ", "),
			match.Context,
		)

		// Send the email concurrently
		go sendEmail(
			&wg, // Pass the waitgroup pointer
			emailConfig.SMTPServer,
			emailConfig.SMTPPort,
			emailConfig.SMTPUser,
			emailConfig.SMTPPass,
			emailConfig.FromEmail,
			emailConfig.ToEmail,
			fmt.Sprintf("ASX Alert: %s - %s", match.Ticker, match.Title),
			emailBody,
		)
	}
	// Block until all email goroutines have completed (either succeeded or failed)
	wg.Wait()
}

func reportMatches(matches []Match) {
	if len(matches) == 0 {
		fmt.Println("\n-------------------------------------------")
		fmt.Println("No new matching keywords found in any announcement today.")
		fmt.Println("-------------------------------------------")
		return
	}

	fmt.Println("\n===========================================")
	fmt.Printf("✅ %d MATCHES FOUND\n", len(matches))
	fmt.Println("===========================================")

	for i, match := range matches {

		consoleOutput := fmt.Sprintf("\n--- MATCH #%d ---\n", i+1) +
			fmt.Sprintf("Ticker: %s\n", match.Ticker) +
			fmt.Sprintf("Title:  %s\n", match.Title) +
			fmt.Sprintf("Price Sensitive: %t\n", match.IsPriceSensitive) +
			fmt.Sprintf("Date:   %s\n", match.DateTime.Format("02 Jan 2006 3:04 PM")) +
			fmt.Sprintf("URL:    %s\n", match.PDFURL) +
			fmt.Sprintf("Keywords: %s\n", strings.Join(match.KeywordsFound, ", ")) +
			fmt.Sprintf("Context Snippet:\n\t%s\n", match.Context)

		fmt.Print(consoleOutput)
	}

	fmt.Println("\n===========================================")
	fmt.Printf("Search complete. History saved to %s.\n", historyFilePath)
	fmt.Println("===========================================")
}

// sendEmail sends a single email with match details.
func sendEmail(wg *sync.WaitGroup, smtpServer string, smtpPort int, smtpUser string, smtpPassword string, fromEmail string, toEmail string, subject string, messageBody string) {
	defer wg.Done() // MUST be called to signal completion

	message := gomail.NewMessage()

	message.SetHeader("From", fromEmail)
	message.SetHeader("To", toEmail)
	message.SetHeader("Subject", subject)

	message.SetBody("text/plain", messageBody)

	dialer := gomail.NewDialer(smtpServer, smtpPort, smtpUser, smtpPassword)
	dialer.Timeout = 10 * time.Second

	if err := dialer.DialAndSend(message); err != nil {
		// Log the error to the console
		log.Printf("Email error: Failed to send email to %s (Subject: %s): %v", toEmail, subject, err)
	} else {
		// Log the success to the console
		log.Printf("Email sent successfully for: %s", subject)
	}
}
