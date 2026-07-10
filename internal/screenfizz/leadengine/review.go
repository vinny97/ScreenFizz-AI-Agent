package leadengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const reviewBatchSize = 20

type reviewProspect struct {
	ID              string `json:"id"`
	BusinessSummary string `json:"business_summary"`
	EmailSubject    string `json:"email_subject"`
	EmailBody       string `json:"email_body"`
	Business        struct {
		BusinessName string `json:"business_name"`
		Email        string `json:"email"`
	} `json:"screenfizz_businesses"`
}

// ReviewProspects presents the next twenty pending email drafts for operator
// approval, editing, or skipping. It never sends an email.
func ReviewProspects(ctx context.Context, cfg Config, input io.Reader, output io.Writer) error {
	prospects, err := nextPendingReviewProspects(ctx, cfg)
	if err != nil {
		return err
	}
	if len(prospects) == 0 {
		_, err := fmt.Fprintln(output, "No pending emails to review.")
		return err
	}

	reader := bufio.NewReader(input)
	for _, prospect := range prospects {
		if err := displayReviewProspect(output, prospect); err != nil {
			return err
		}
		for {
			action, err := readReviewLine(reader, output, "Action [A]pprove, [E]dit, [S]kip: ")
			if err != nil {
				return err
			}
			switch strings.ToUpper(strings.TrimSpace(action)) {
			case "A":
				if err := updateReviewProspect(ctx, cfg, prospect.ID, map[string]any{"status": "approved"}); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(output, "Approved.")
			case "E":
				if err := editReviewProspect(ctx, cfg, reader, output, prospect); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(output, "Saved for review.")
			case "S":
				if err := updateReviewProspect(ctx, cfg, prospect.ID, map[string]any{"status": "skipped"}); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(output, "Skipped.")
			default:
				_, _ = fmt.Fprintln(output, "Choose A, E, or S.")
				continue
			}
			break
		}
	}
	return nil
}

func nextPendingReviewProspects(ctx context.Context, cfg Config) ([]reviewProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select": {"id,business_summary,email_subject,email_body,screenfizz_businesses!inner(business_name,email)"},
		"status": {"eq.pending_review"},
		"order":  {"created_at.asc"},
		"limit":  {fmt.Sprintf("%d", reviewBatchSize)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz review queue request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list pending ScreenFizz reviews: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read pending ScreenFizz reviews response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list pending ScreenFizz reviews: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []reviewProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode pending ScreenFizz reviews response: %w", err)
	}
	return prospects, nil
}

func displayReviewProspect(output io.Writer, prospect reviewProspect) error {
	_, err := fmt.Fprintf(output, "\nBusiness: %s\nEmail: %s\nAI summary: %s\nSubject: %s\nEmail body:\n%s\n\n",
		prospect.Business.BusinessName,
		prospect.Business.Email,
		prospect.BusinessSummary,
		prospect.EmailSubject,
		prospect.EmailBody)
	return err
}

func editReviewProspect(ctx context.Context, cfg Config, reader *bufio.Reader, output io.Writer, prospect reviewProspect) error {
	subject, err := readReviewLine(reader, output, "New subject (leave blank to keep): ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(subject) == "" {
		subject = prospect.EmailSubject
	}
	body, err := readReviewLine(reader, output, "New body (leave blank to keep; finish a replacement with a single . line): ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		body = prospect.EmailBody
	} else {
		lines := []string{body}
		for {
			line, err := readReviewLine(reader, output, "")
			if err != nil {
				return err
			}
			if line == "." {
				break
			}
			lines = append(lines, line)
		}
		body = strings.Join(lines, "\n")
	}
	return updateReviewProspect(ctx, cfg, prospect.ID, map[string]any{
		"email_subject": subject,
		"email_body":    body,
		"status":        "pending_review",
	})
}

func readReviewLine(reader *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if prompt != "" {
		if _, err := fmt.Fprint(output, prompt); err != nil {
			return "", err
		}
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func updateReviewProspect(ctx context.Context, cfg Config, prospectID string, values map[string]any) error {
	body, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode ScreenFizz review update: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz review update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz review update: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz review update response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz review update: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
