package asx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func getSnippet(fullText string, keyword string) string {
	const contextSize = 50

	lowerText := strings.ToLower(fullText)
	lowerKeyword := strings.ToLower(keyword)

	index := strings.Index(lowerText, lowerKeyword)
	if index == -1 {
		return ""
	}

	start := index - contextSize
	if start < 0 {
		start = 0
	}

	end := index + len(lowerKeyword) + contextSize
	if end > len(fullText) {
		end = len(fullText)
	}

	snippet := fullText[start:end]

	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(fullText) {
		snippet = snippet + " ..."
	}

	return strings.ReplaceAll(snippet, "\n", " ")
}

func extractTextFromPDF(asxTriggerURL string) (string, error) {
	resp, err := client.Get(asxTriggerURL)
	if err != nil {
		return "", fmt.Errorf("failed initial GET to %s: %w", asxTriggerURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	bodyString := string(bodyBytes)

	finalPDFURL := asxTriggerURL

	// Check and bypass the T&C form if present
	if strings.Contains(bodyString, asxTermsAction) {
		re := regexp.MustCompile(`name="pdfURL"\s+value="(.*?)"`)
		match := re.FindStringSubmatch(bodyString)

		if len(match) < 2 {
			return "", fmt.Errorf("T&C form detected, but could not find the hidden 'pdfURL' field")
		}

		directPDFURL := match[1]

		// Submit the "Agree and proceed" form via POST to set the session cookie.
		formValues := url.Values{
			"pdfURL":                  {directPDFURL},
			"showAnnouncementPDFForm": {"Agree and proceed"},
		}

		termsURL := asxBaseURL + asxTermsAction
		_, err = client.PostForm(termsURL, formValues)
		if err != nil {
			log.Printf("Warning: T&C POST submission failed or redirected unexpectedly: %v", err)
		}

		finalPDFURL = directPDFURL
	}

	// Download the actual PDF content bytes
	pdfResp, err := client.Get(finalPDFURL)
	if err != nil {
		return "", fmt.Errorf("failed to download final PDF from %s: %w", finalPDFURL, err)
	}
	defer pdfResp.Body.Close()

	if pdfResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download PDF: received status code %d from %s", pdfResp.StatusCode, finalPDFURL)
	}

	pdfBytes, err := io.ReadAll(pdfResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF response body: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), pdfProcessingTimeout)
	defer cancel()

	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		tmpFile, err := os.CreateTemp("", "asx_pdf_*.pdf")
		if err != nil {
			errChan <- fmt.Errorf("failed to create temporary file: %w", err)
			return
		}
		tmpFileName := tmpFile.Name()
		tmpFile.Close()

		defer os.Remove(tmpFileName)

		if err := os.WriteFile(tmpFileName, pdfBytes, 0644); err != nil {
			errChan <- fmt.Errorf("failed to write PDF bytes to temp file: %w", err)
			return
		}

		// Execute pdftotext command
		cmd := exec.CommandContext(ctx, "pdftotext", "-raw", tmpFileName, "-")

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			errMsg := fmt.Sprintf("pdftotext failed: %v. Stderr: %s", err, strings.TrimSpace(stderr.String()))
			if strings.Contains(errMsg, "no such file or directory") || strings.Contains(errMsg, "not found") {
				errChan <- fmt.Errorf("pdftotext binary not found. Please ensure poppler-utils is installed. Error: %s", strings.TrimSpace(stderr.String()))
			} else {
				errChan <- fmt.Errorf(errMsg)
			}
			return
		}

		text := out.String()

		if strings.TrimSpace(text) == "" {
			errChan <- fmt.Errorf("pdftotext extracted empty text string. File may be image-based or protected")
			return
		}

		resultChan <- text
	}()

	select {
	case text := <-resultChan:
		return text, nil
	case err := <-errChan:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("PDF text extraction timed out after %s", pdfProcessingTimeout)
	}
}
