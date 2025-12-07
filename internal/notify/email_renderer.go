package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// HTMLEmailRenderer renders notifications as HTML emails with a plain text fallback.
type HTMLEmailRenderer struct {
	tmpl *template.Template
}

// NewHTMLEmailRenderer creates a renderer with the default email template.
func NewHTMLEmailRenderer() *HTMLEmailRenderer {
	t := template.Must(template.New("email").Parse(emailHTMLTemplate))
	return &HTMLEmailRenderer{tmpl: t}
}

// Render produces an HTML email with plain text alternative.
func (r *HTMLEmailRenderer) Render(data NotificationData) (*RenderedMessage, error) {
	subject := fmt.Sprintf("ASX Alert: %s - %s", data.Match.Ticker, data.Match.Title)

	var htmlBuf bytes.Buffer
	if err := r.tmpl.Execute(&htmlBuf, data); err != nil {
		return nil, fmt.Errorf("failed to render HTML template: %w", err)
	}

	return &RenderedMessage{
		Subject: subject,
		Text:    renderPlainText(data),
		HTML:    htmlBuf.String(),
	}, nil
}

// renderPlainText produces a readable plain text version for email clients that don't support HTML.
func renderPlainText(data NotificationData) string {
	m := data.Match
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s - %s\n", m.Ticker, m.Title))
	sb.WriteString(strings.Repeat("=", 50) + "\n\n")

	if m.IsPriceSensitive {
		sb.WriteString("⚡ PRICE SENSITIVE\n\n")
	}

	sb.WriteString(fmt.Sprintf("Date: %s\n", m.DateTime.Format("02 Jan 2006 3:04 PM")))
	sb.WriteString(fmt.Sprintf("URL: %s\n", m.PDFURL))

	if len(m.KeywordsFound) > 0 {
		sb.WriteString(fmt.Sprintf("Keywords: %s\n", strings.Join(m.KeywordsFound, ", ")))
	}
	sb.WriteString("\n")

	if m.Context != "" {
		sb.WriteString("CONTEXT\n")
		sb.WriteString(strings.Repeat("-", 20) + "\n")
		sb.WriteString(m.Context + "\n\n")
	}

	if data.Analysis != nil {
		if len(data.Analysis.Summary) > 0 {
			sb.WriteString("AI SUMMARY\n")
			sb.WriteString(strings.Repeat("-", 20) + "\n")
			for _, s := range data.Analysis.Summary {
				sb.WriteString(fmt.Sprintf("• %s\n", s))
			}
			sb.WriteString("\n")
		}

		if len(data.Analysis.PotentialCatalysts) > 0 {
			sb.WriteString("POTENTIAL CATALYSTS\n")
			sb.WriteString(strings.Repeat("-", 20) + "\n")
			for _, c := range data.Analysis.PotentialCatalysts {
				sb.WriteString(fmt.Sprintf("• [%s] %s\n", c.Category, c.Details))
			}
			sb.WriteString("\n")
		}

	}

	return sb.String()
}
