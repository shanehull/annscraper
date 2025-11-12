package notify

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/shanehull/annscraper/internal/types"

	gomail "gopkg.in/mail.v2"
)

type EmailConfig struct {
	SMTPServer string
	SMTPPort   int
	SMTPUser   string
	SMTPPass   string
	FromEmail  string
	ToEmail    string
	Enabled    bool
}

func ReportMatches(matches []types.Match, historyFilePath string) {
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

func EmailMatches(matches []types.Match, emailConfig EmailConfig) { // Uses local EmailConfig
	if !emailConfig.Enabled {
		return
	}

	log.Printf("Emailing matches (SMTP: %s:%d).", emailConfig.SMTPServer, emailConfig.SMTPPort)

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

		go sendEmail(
			&wg,
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
	wg.Wait()
}

func sendEmail(wg *sync.WaitGroup, smtpServer string, smtpPort int, smtpUser string, smtpPassword string, fromEmail string, toEmail string, subject string, messageBody string) {
	defer wg.Done()

	message := gomail.NewMessage()

	message.SetHeader("From", fromEmail)
	message.SetHeader("To", toEmail)
	message.SetHeader("Subject", subject)
	message.SetBody("text/plain", messageBody)

	dialer := gomail.NewDialer(smtpServer, smtpPort, smtpUser, smtpPassword)
	dialer.Timeout = 10 * time.Second

	if err := dialer.DialAndSend(message); err != nil {
		log.Printf("Email error: Failed to send email to %s (Subject: %s): %v", toEmail, subject, err)
	} else {
		log.Printf("Email sent successfully for: %s", subject)
	}
}
