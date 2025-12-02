/*
Package types defines the core data structures used in the annscraper application.
*/
package types

import (
	"time"

	"github.com/shanehull/annscraper/internal/ai"
)

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
	TickerMatched bool
	Context       string
}

type AnnotatedMatch struct {
	Match    Match
	Analysis *ai.AIAnalysis
}
