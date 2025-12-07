package notify

const emailHTMLTemplate = `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Match.Ticker}} – {{.Match.Title}}</title>
  <style>
    body {
      margin: 0;
      padding: 24px;
      background-color: #f3f4f6;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      color: #111827;
      line-height: 1.5;
    }

    .container {
      max-width: 640px;
      margin: 0 auto;
      background: #ffffff;
      border-radius: 8px;
      border: 1px solid #e5e7eb;
      overflow: hidden;
    }

    .header {
      padding: 20px 24px;
      background: linear-gradient(135deg, #463737 0%, #37393b 100%);
      color: #ffffff;
    }

    .ticker {
      font-size: 24px;
      font-weight: 700;
      letter-spacing: 0.05em;
      margin-bottom: 4px;
    }

    .title {
      font-size: 15px;
      opacity: 0.9;
    }

    .badge {
      display: inline-block;
      margin-top: 8px;
      padding: 4px 10px;
      font-size: 11px;
      font-weight: 600;
      border-radius: 4px;
      background: #f97316;
      color: #ffffff;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    .section {
      padding: 16px 24px;
      border-top: 1px solid #f3f4f6;
    }

    .section-title {
      font-size: 11px;
      font-weight: 700;
      color: #6b7280;
      text-transform: uppercase;
      letter-spacing: 0.1em;
      margin-bottom: 12px;
    }

    .meta-grid {
      display: table;
      width: 100%;
      font-size: 14px;
    }

    .meta-row {
      display: table-row;
    }

    .meta-label {
      display: table-cell;
      padding: 6px 16px 6px 0;
      color: #6b7280;
      font-weight: 500;
      white-space: nowrap;
      width: 100px;
    }

    .meta-value {
      display: table-cell;
      padding: 6px 0;
      color: #111827;
    }

    .keywords-list {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
      margin: 0;
      padding: 0;
      list-style: none;
    }

    .keyword-tag {
      display: inline-block;
      padding: 3px 10px;
      font-size: 12px;
      font-weight: 500;
      background: #e0f2fe;
      color: #0369a1;
      border-radius: 4px;
    }

    .summary-list,
    .catalyst-list {
      margin: 0;
      padding-left: 20px;
      font-size: 14px;
    }

    .summary-list li,
    .catalyst-list li {
      margin-bottom: 8px;
      padding-left: 4px;
    }

    .catalyst-category {
      display: inline-block;
      padding: 3px 6px;
      font-size: 10px;
      font-weight: 600;
      background: #fef3c7;
      color: #92400e;
      border-radius: 3px;
      text-transform: uppercase;
      letter-spacing: 0.03em;
      margin-right: 2px;
    }

    .context-box {
      background: #f9fafb;
      border-left: 3px solid #463737;
      padding: 12px 16px;
      font-size: 13px;
      color: #374151;
      border-radius: 0 4px 4px 0;
    }

    .cta-button {
      display: inline-block;
      margin-top: 12px;
      padding: 10px 20px;
      font-size: 14px;
      font-weight: 600;
	  color: #ffffff !important;
      background: #463737;
      border-radius: 6px;
      text-decoration: none;
    }

    .footer {
      padding: 16px 24px;
      font-size: 12px;
      color: #9ca3af;
      text-align: center;
      background: #f9fafb;
      border-top: 1px solid #f3f4f6;
    }

    a {
      color: #0b3d91;
      text-decoration: none;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <div class="ticker">{{.Match.Ticker}}</div>
      <div class="title">{{.Match.Title}}</div>
      {{if .Match.IsPriceSensitive}}
      <span class="badge">⚡ Price Sensitive</span>
      {{end}}
    </div>

    <div class="section">
      <div class="section-title">Announcement Details</div>
      <div class="meta-grid">
        <div class="meta-row">
          <div class="meta-label">Date</div>
          <div class="meta-value">{{.Match.DateTime.Format "02 Jan 2006 3:04 PM"}}</div>
        </div>
        {{if .Match.KeywordsFound}}
        <div class="meta-row">
          <div class="meta-label">Keywords</div>
          <div class="meta-value">
            <div class="keywords-list">
              {{range .Match.KeywordsFound}}
              <span class="keyword-tag">{{.}}</span>
              {{end}}
            </div>
          </div>
        </div>
        {{end}}
      </div>
      <a href="{{.Match.PDFURL}}" class="cta-button" target="_blank" rel="noopener">
        View ASX Announcement →
      </a>
    </div>

    {{if .Match.Context}}
    <div class="section">
      <div class="section-title">Context Snippet</div>
      <div class="context-box">{{.Match.Context}}</div>
    </div>
    {{end}}

    {{if .Analysis}}
      {{if .Analysis.Summary}}
      <div class="section">
        <div class="section-title">AI Summary</div>
        <ul class="summary-list">
          {{range .Analysis.Summary}}
          <li>{{.}}</li>
          {{end}}
        </ul>
      </div>
      {{end}}

      {{if .Analysis.PotentialCatalysts}}
      <div class="section">
        <div class="section-title">Potential Catalysts</div>
        <ul class="catalyst-list">
          {{range .Analysis.PotentialCatalysts}}
          <li>
            <span class="catalyst-category">{{.Category}}</span>
            <span>{{.Details}}</span>
          </li>
          {{end}}
        </ul>
      </div>
      {{end}}
    {{end}}

    <div class="footer">
      Generated by <a href=https://github.com/shanehull/annscraper  target="_blank" rel="noopener">annscraper</a>
    </div>
  </div>
</body>
</html>`
