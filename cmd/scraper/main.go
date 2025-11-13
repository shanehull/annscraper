package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/shanehull/annscraper/internal/asx"
	"github.com/shanehull/annscraper/internal/history"
	"github.com/shanehull/annscraper/internal/notify"
	"github.com/shanehull/annscraper/internal/types"
)

var timezone = "Australia/Sydney"

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

func main() {
	keywordStr := flag.String("keywords", "", "Comma-separated list of keywords or exact phrases")
	filterPriceSensitive := flag.Bool("price-sensitive", false, "(-s) Process ONLY price sensitive announcements")
	smtpServer := flag.String("smtp-server", "", "SMTP server address")
	smtpPort := flag.Int("smtp-port", 587, "SMTP server port")
	smtpUser := flag.String("smtp-user", "", "SMTP username (email address)")
	smtpPass := flag.String("smtp-pass", "", "SMTP password or App Password")
	toEmail := flag.String("to-email", "", "Recipient email address")
	fromEmail := flag.String("from-email", "", "Sender email address (must match user/auth)")

	flag.Parse()

	if *keywordStr == "" {
		fmt.Println("Error: Keywords are required.")
		fmt.Println("Usage: go run ./cmd/scraper/main.go -keywords 'keyword1' [-s=false] --smtp-server=... --to-email=...")
		os.Exit(1)
	}

	keywords := parseKeywords(*keywordStr)

	emailConfig := notify.EmailConfig{
		SMTPServer: *smtpServer,
		SMTPPort:   *smtpPort,
		SMTPUser:   *smtpUser,
		SMTPPass:   *smtpPass,
		ToEmail:    *toEmail,
		FromEmail:  *fromEmail,
		Enabled:    (*smtpServer != "" && *smtpUser != "" && *smtpPass != "" && *toEmail != "" && *fromEmail != ""),
	}

	historyManager, err := history.NewManager(timezone)
	if err != nil {
		fmt.Printf("Fatal error setting up history: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting ASX Scraper. Searching for keywords/phrases: %s\n", strings.Join(keywords, ", "))

	announcements, err := asx.ScrapeAnnouncements(*filterPriceSensitive)
	if err != nil {
		fmt.Printf("Fatal error during scraping: %v\n", err)
		os.Exit(1)
	}

	totalAnns := len(announcements)
	if totalAnns == 0 {
		fmt.Println("No announcements found today or scraping failed.")
		historyManager.RecordMatches(nil)
		return
	}
	fmt.Printf("Found %d total announcements (price-sensitive: %t). Starting PDF download and search...\n", totalAnns, *filterPriceSensitive)

	filterFunc := func(ann types.Announcement, foundKeywords []string) []string {
		return historyManager.FilterNewMatches(ann, foundKeywords)
	}

	newMatches := asx.ProcessAnnouncements(announcements, keywords, filterFunc)

	if len(newMatches) == 0 {
		fmt.Println("\n-------------------------------------------")
		fmt.Println("No new matching keywords found in any announcement today.")
		fmt.Println("-------------------------------------------")
	} else {
		notify.ReportMatches(newMatches, historyManager.HistoryFilePath())
		if emailConfig.Enabled {
			notify.EmailMatches(newMatches, emailConfig)
		}
	}

	historyManager.RecordMatches(newMatches)
}
