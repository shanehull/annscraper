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

const timezone = "Australia/Sydney"

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

var (
	keywordStr           = flag.String("keywords", "", "(-k) Comma-separated list of keywords or exact phrases")
	filterPriceSensitive = flag.Bool("price-sensitive", false, "(-s) Process ONLY price sensitive announcements")
	scrapePrevious       = flag.Bool("previous", false, "(-p) Scrape previous business days announcements")

	smtpServer = flag.String("smtp-server", "smtp.gmail.com", "SMTP server address (default: smtp.gmail.com)")
	smtpPort   = flag.Int("smtp-port", 587, "SMTP server port (default: 587)")
	smtpUser   = flag.String("smtp-user", "", "SMTP username (email address)")
	smtpPass   = flag.String("smtp-pass", "", "SMTP password or App Password")
	toEmail    = flag.String("to-email", "", "Recipient email address")
	fromEmail  = flag.String("from-email", "", "Sender email address (default: smtp-user)")
)

func init() {
	flag.StringVar(keywordStr, "k", "", "(-k) Comma-separated list of keywords or exact phrases (shorthand)")
	flag.BoolVar(filterPriceSensitive, "s", false, "(-s) Process ONLY price sensitive announcements (shorthand)")
	flag.BoolVar(scrapePrevious, "p", false, "(-p) Scrape previous business days announcements (shorthand)")

	flag.Usage = func() {
		flagSet := flag.CommandLine
		fmt.Printf("Custom Usage of %s:\n", "myprogram")

		order := []string{
			"keywords",
			"price-sensitive",
			"previous",
			"smtp-server",
			"smtp-port",
			"smtp-user",
			"smtp-pass",
			"to-email",
			"from-email",
		}

		for _, name := range order {
			f := flagSet.Lookup(name)
			if f != nil {
				fmt.Printf("  -%s\n", f.Name)
				fmt.Printf("    %s\n", f.Usage)
			}
		}
	}
}

func main() {
	flag.Parse()

	if *keywordStr == "" {
		fmt.Println("Error: Keywords are required.")
		fmt.Println("Usage: annscraper -keywords 'keyword1,keyword2' [-s] --smtp-server=... --to-email=...")
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
		Enabled:    (*smtpServer != "" && *smtpUser != "" && *smtpPass != "" && *toEmail != ""),
	}

	if emailConfig.FromEmail == "" && emailConfig.SMTPUser != "" {
		emailConfig.FromEmail = emailConfig.SMTPUser
	}

	historyManager, err := history.NewManager(timezone)
	if err != nil {
		fmt.Printf("Fatal error setting up history: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting ASX Scraper. Searching for keywords/phrases: %s\n", strings.Join(keywords, ", "))

	announcements, err := asx.ScrapeAnnouncements(*filterPriceSensitive, *scrapePrevious)
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
