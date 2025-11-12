package types

import (
	"time"
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
	Context       string
}
