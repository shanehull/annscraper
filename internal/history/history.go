/*
Package history provides functionality to manage the history of reported announcements.
*/
package history

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shanehull/annscraper/internal/types"
)

const (
	historyFileName        = "asx_report_history.json"
	historyDirName         = "annscraper"
	tickerMatchPlaceholder = "__TICKER_MATCHED__"
)

type History struct {
	ReportDate      string
	ReportedMatches map[string]map[string]bool
}

type Manager struct {
	history         History
	mutex           sync.Mutex
	historyFilePath string
	reportLocation  *time.Location
}

func NewManager(tzName string) (*Manager, error) {
	historyDir := filepath.Join(os.TempDir(), historyDirName)
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create temporary history directory %s: %w", historyDir, err)
	}
	filePath := filepath.Join(historyDir, historyFileName)

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("invalid time zone name '%s': %w", tzName, err)
	}

	m := &Manager{
		historyFilePath: filePath,
		reportLocation:  loc,
	}

	m.loadHistory()
	return m, nil
}

func (m *Manager) loadHistory() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	today := m.getCurrentReportDate()
	m.history = History{
		ReportDate:      today,
		ReportedMatches: make(map[string]map[string]bool),
	}

	data, err := os.ReadFile(m.historyFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("History file %s not found. Starting fresh report.", m.historyFilePath)
			return
		}
		log.Printf("Error reading history file (%s): %v. Starting fresh report.", m.historyFilePath, err)
		return
	}

	var loadedHistory History
	if err := json.Unmarshal(data, &loadedHistory); err != nil {
		log.Printf("Error unmarshalling history JSON: %v. Starting fresh report.", err)
		return
	}

	if loadedHistory.ReportDate == today {
		m.history = loadedHistory
		log.Printf("Loaded %d reported matches for today (%s).", len(m.history.ReportedMatches), today)
	} else {
		log.Printf("History is from %s. Starting new report history for today (%s).", loadedHistory.ReportDate, today)
	}
}

func (m *Manager) saveHistory() {
	m.history.ReportDate = m.getCurrentReportDate()

	data, err := json.MarshalIndent(m.history, "", "  ")
	if err != nil {
		log.Printf("Error marshalling history for save: %v", err)
		return
	}

	if err := os.WriteFile(m.historyFilePath, data, 0o644); err != nil {
		log.Printf("Error writing history file %s: %v", m.historyFilePath, err)
	} else {
		log.Printf("Successfully saved report history to %s.", m.historyFilePath)
	}
}

func (m *Manager) FilterNewMatches(ann types.Announcement, foundKeywords []string, isTickerMatch bool) []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	key := ann.Ticker + "|" + ann.Title
	reportedKws, exists := m.history.ReportedMatches[key]

	if isTickerMatch && len(foundKeywords) == 0 {
		if exists && reportedKws[tickerMatchPlaceholder] {
			return nil
		}

		return []string{tickerMatchPlaceholder}
	}

	if len(foundKeywords) == 0 {
		return nil
	}

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

func (m *Manager) RecordMatches(matches []types.Match) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, match := range matches {
		key := match.Ticker + "|" + match.Title

		if m.history.ReportedMatches[key] == nil {
			m.history.ReportedMatches[key] = make(map[string]bool)
		}

		if len(match.KeywordsFound) == 0 && match.TickerMatched {
			m.history.ReportedMatches[key][tickerMatchPlaceholder] = true
		}

		for _, kw := range match.KeywordsFound {
			m.history.ReportedMatches[key][kw] = true
		}
	}
	m.saveHistory()
}

func (m *Manager) HistoryFilePath() string {
	return m.historyFilePath
}

func (m *Manager) getCurrentReportDate() string {
	return time.Now().In(m.reportLocation).Format("2006-01-02")
}
