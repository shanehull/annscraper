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

	start := max(index-contextSize, 0)

	end := min(index+len(lowerKeyword)+contextSize, len(fullText))

	snippet := fullText[start:end]

	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(fullText) {
		snippet = snippet + " ..."
	}

	return strings.ReplaceAll(snippet, "\n", " ")
}

func extractTextFromPDF(pdfURL string) (string, error) {
	resp, err := client.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed initial GET to %s: %w", pdfURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("Warning: failed to close response body for %s: %v", pdfURL, cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download PDF: received status code %d from %s", resp.StatusCode, pdfURL)
	}

	pdfBytes, err := io.ReadAll(resp.Body)
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
		err = tmpFile.Close()
		if err != nil {
			errChan <- fmt.Errorf("failed to close temporary file: %w", err)
		}
		defer func() {
			if rerr := os.Remove(tmpFileName); rerr != nil {
				log.Printf("Warning: failed to remove temp file %s: %v", tmpFileName, rerr)
			}
		}()

		if err := os.WriteFile(tmpFileName, pdfBytes, 0o644); err != nil {
			errChan <- fmt.Errorf("failed to write PDF bytes to temp file: %w", err)
			return
		}

		cmd := exec.CommandContext(ctx, "pdftotext", "-raw", tmpFileName, "-")

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			cmdErr := fmt.Errorf("pdftotext failed: %v. Stderr: %s", err, strings.TrimSpace(stderr.String()))
			if strings.Contains(cmdErr.Error(), "not found") {
				errChan <- fmt.Errorf("pdftotext binary not found. Please ensure poppler-utils is installed. Error: %s", strings.TrimSpace(stderr.String()))
			} else {
				errChan <- cmdErr
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

func getPDFURLFromDoURL(doURL string) (string, error) {
	resp, err := client.Get(doURL)
	if err != nil {
		return "", fmt.Errorf("failed initial GET to %s: %w", doURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("Warning: failed to close response body for %s: %v", doURL, cerr)
		}
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	bodyString := string(bodyBytes)

	if strings.Contains(bodyString, asxTermsAction) {
		re := regexp.MustCompile(`name="pdfURL"\s+value="(.*?)"`)
		match := re.FindStringSubmatch(bodyString)

		if len(match) < 2 {
			return "", fmt.Errorf("T&C form detected, but could not find the hidden 'pdfURL' field")
		}

		directPDFURL := match[1]

		// Submit the "Agree and proceed" form via POST to set the session cookie
		formValues := url.Values{
			"pdfURL":                  {directPDFURL},
			"showAnnouncementPDFForm": {"Agree and proceed"},
		}

		termsURL := asxBaseURL + asxTermsAction
		_, err = client.PostForm(termsURL, formValues)
		if err != nil {
			log.Printf("Warning: T&C POST submission failed or redirected unexpectedly: %v", err)
		}

		return directPDFURL, nil
	}

	return "", fmt.Errorf("no PDF URL found at ASX announcement page %s", doURL)
}
