/*
Package notify handles reporting of matches via console output and email notifications.
*/
package notify

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/shanehull/annscraper/internal/ai"
	"github.com/shanehull/annscraper/internal/types"
)

type NotificationData struct {
	Match    types.Match
	Analysis *ai.AIAnalysis
}

type RenderedMessage struct {
	Subject string
	Text    string
	HTML    string
}

type Renderer interface {
	Render(data NotificationData) (*RenderedMessage, error)
}

type Sender interface {
	Send(msg *RenderedMessage) error
}

const (
	dim    = "\033[2m"
	bold   = "\033[1m"
	reset  = "\033[0m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
	green  = "\033[32m"
	orange = "\033[38;5;208m"
)

// ReportMatches prints matches to the console.
func ReportMatches(matches []types.AnnotatedMatch, historyFilePath string) {
	if len(matches) == 0 {
		fmt.Printf("\n%s──────────────────────────────────────────%s\n", dim, reset)
		fmt.Println("  No new matching keywords found today.")
		fmt.Printf("%s──────────────────────────────────────────%s\n\n", dim, reset)
		return
	}

	headerText := fmt.Sprintf("%d MATCH(ES) FOUND", len(matches))
	boxWidth := 42
	padding := boxWidth - len(headerText) - 3

	fmt.Printf("\n%s╔%s╗%s\n", cyan, strings.Repeat("═", boxWidth), reset)
	fmt.Printf("%s║%s  %s%s%s%s %s║%s\n", cyan, reset, bold, headerText, reset, strings.Repeat(" ", padding), cyan, reset)
	fmt.Printf("%s╚%s╝%s\n", cyan, strings.Repeat("═", boxWidth), reset)

	for i, am := range matches {
		printMatch(i+1, am)
	}

	fmt.Printf("\n%s──────────────────────────────────────────%s\n", dim, reset)
	fmt.Printf("%sHistory saved to %s%s\n", dim, historyFilePath, reset)
}

func printMatch(num int, am types.AnnotatedMatch) {
	m := am.Match

	// Header
	priceSensitive := ""
	if m.IsPriceSensitive {
		priceSensitive = fmt.Sprintf(" %s⚡ PRICE SENSITIVE%s", orange, reset)
	}
	fmt.Printf("\n%s┌─ %s#%d%s %s%s%s%s\n", dim, bold, num, reset, cyan+bold, m.Ticker, reset, priceSensitive)

	// Title
	fmt.Printf("%s│%s  %s\n", dim, reset, m.Title)

	// Metadata
	fmt.Printf("%s│%s\n", dim, reset)
	fmt.Printf("%s│%s  %sDate%s      %s\n", dim, reset, dim, reset, m.DateTime.Format("02 Jan 2006 3:04 PM"))
	if len(m.KeywordsFound) > 0 {
		fmt.Printf("%s│%s  %sKeywords%s  %s\n", dim, reset, dim, reset, strings.Join(m.KeywordsFound, ", "))
	}
	fmt.Printf("%s│%s  %sURL%s       %s\n", dim, reset, dim, reset, m.PDFURL)

	// Context
	if m.Context != "" {
		fmt.Printf("%s│%s\n", dim, reset)
		fmt.Printf("%s│%s  %s▸ Context%s\n", dim, reset, yellow, reset)
		printIndented(m.Context, 5)
	}

	// AI Summary
	if am.Analysis != nil {
		if len(am.Analysis.Summary) > 0 {
			fmt.Printf("%s│%s\n", dim, reset)
			fmt.Printf("%s│%s  %s▸ AI Summary%s\n", dim, reset, green, reset)
			for _, s := range am.Analysis.Summary {
				fmt.Printf("%s│%s    • %s\n", dim, reset, s)
			}
		}

		if len(am.Analysis.PotentialCatalysts) > 0 {
			fmt.Printf("%s│%s\n", dim, reset)
			fmt.Printf("%s│%s  %s▸ Potential Catalysts%s\n", dim, reset, green, reset)
			for _, c := range am.Analysis.PotentialCatalysts {
				fmt.Printf("%s│%s    %s[%s]%s %s\n", dim, reset, dim, c.Category, reset, c.Details)
			}
		}
	}

	fmt.Printf("%s└──────────────────────────────────────────%s\n", dim, reset)
}

func printIndented(text string, indent int) {
	prefix := strings.Repeat(" ", indent)
	lines := strings.SplitSeq(text, "\n")
	for line := range lines {
		if line != "" {
			fmt.Printf("%s│%s%s%s\n", dim, reset, prefix, line)
		}
	}
}

// EmailMatches sends each match as a rich HTML email.
func EmailMatches(matches []types.AnnotatedMatch, cfg EmailConfig) {
	if !cfg.Enabled || len(matches) == 0 {
		return
	}

	log.Printf("Emailing %d matches (SMTP: %s:%d)", len(matches), cfg.SMTPServer, cfg.SMTPPort)

	renderer := NewHTMLEmailRenderer()
	sender := NewEmailSender(cfg)

	var wg sync.WaitGroup
	for _, am := range matches {
		wg.Go(func() {
			data := NotificationData{
				Match:    am.Match,
				Analysis: am.Analysis,
			}

			msg, err := renderer.Render(data)
			if err != nil {
				log.Printf("Email render error for %s: %v", am.Match.Ticker, err)
				return
			}

			_ = sender.Send(msg)
		})
	}
	wg.Wait()
}
