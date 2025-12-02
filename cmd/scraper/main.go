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

func parseTickers(s string) []string {
	parts := strings.Split(s, ",")
	var tickers []string
	for _, part := range parts {
		trimmed := strings.ToUpper(strings.TrimSpace(part))
		if trimmed != "" {
			tickers = append(tickers, trimmed)
		}
	}
	return tickers
}

var (
	keywordsStr          = flag.String("keywords", "", "(-k) Comma-separated list of keywords or exact phrases to match")
	tickersStr           = flag.String("tickers", "", "(-t) Comma-separated list of tickers to match (takes precedence over keywords)")
	filterPriceSensitive = flag.Bool("price-sensitive", false, "(-s) Process ONLY price sensitive announcements")
	scrapePrevious       = flag.Bool("previous", false, "(-p) Scrape previous business days announcements")

	modelName    = flag.String("model", "gemini-3-pro-preview", "Gemini model to use for analysis (e.g., 'gemini-2.5-flash', 'gemini-3-pro-preview')")
	geminiAPIKey = flag.String("gemini-key", "", "Gemini API Key for generating AI summaries")

	smtpServer = flag.String("smtp-server", "smtp.gmail.com", "SMTP server address (default: smtp.gmail.com)")
	smtpPort   = flag.Int("smtp-port", 587, "SMTP server port (default: 587)")
	smtpUser   = flag.String("smtp-user", "", "SMTP username (email address)")
	smtpPass   = flag.String("smtp-pass", "", "SMTP password or App Password")
	toEmail    = flag.String("to-email", "", "Recipient email address")
	fromEmail  = flag.String("from-email", "", "Sender email address (default: smtp-user)")
)

func init() {
	flag.StringVar(keywordsStr, "k", "", "(-k) Comma-separated list of keywords or exact phrases (shorthand)")
	flag.StringVar(tickersStr, "t", "", "(-t) Comma-separated list of tickers to match (takes precedence over keywords) (shorthand)")
	flag.BoolVar(filterPriceSensitive, "s", false, "(-s) Process ONLY price sensitive announcements (shorthand)")
	flag.BoolVar(scrapePrevious, "p", false, "(-p) Scrape previous business days announcements (shorthand)")

	flag.StringVar(modelName, "m", "gemini-3-pro-preview", "Gemini model to use for analysis (e.g., 'gemini-2.5-flash', 'gemini-3-pro-preview') (shorthand)")
	flag.StringVar(geminiAPIKey, "g", "", "Gemini API Key for generating AI summaries (shorthand)")

	flag.Usage = func() {
		flagSet := flag.CommandLine
		fmt.Printf("Custom Usage of %s:\n", "annscraper")

		order := []string{
			"keywords",
			"tickers",
			"price-sensitive",
			"previous",
			"gemini-key",
			"model",
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

	if *keywordsStr == "" && *tickersStr == "" {
		fmt.Println("Error: Keywords or tickers are required.")
		fmt.Println("Usage: annscraper -keywords 'keyword1,keyword2' -tickers 'cba,bhp' [-s] --smtp-server=... --to-email=...")
		os.Exit(1)
	}

	keywords := parseKeywords(*keywordsStr)
	tickers := parseTickers(*tickersStr)

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

	tickersLog := ""
	keywordsLog := ""
	if keywords != nil {
		tickersLog = fmt.Sprintf("Filtering for keywords/phrases: [%s];", strings.TrimSpace(*keywordsStr))
	}
	if tickers != nil {
		keywordsLog = fmt.Sprintf(" Filtering for tickers: [%s]", strings.ToUpper(strings.TrimSpace(*tickersStr)))
	}
	fmt.Printf("Starting ASX Scraper. %s%s\n", tickersLog, keywordsLog)

	announcements, err := asx.ScrapeDailyFeed(*filterPriceSensitive, *scrapePrevious)
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

	annotatedMatches := asx.ProcessAnnouncements(announcements, keywords, tickers, filterFunc, *geminiAPIKey, *modelName)

	var coreMatches []types.Match
	for _, am := range annotatedMatches {
		coreMatches = append(coreMatches, am.Match)
	}

	if len(annotatedMatches) == 0 {
		fmt.Println("\n-------------------------------------------")
		fmt.Println("No new matching keywords found in any announcement today.")
		fmt.Println("-------------------------------------------")
	} else {
		// Report new matches using the annotated data
		notify.ReportMatches(annotatedMatches, historyManager.HistoryFilePath())

		if emailConfig.Enabled {
			notify.EmailMatches(annotatedMatches, emailConfig)
		}
	}

	historyManager.RecordMatches(coreMatches)
}
