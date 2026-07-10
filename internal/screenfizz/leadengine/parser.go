package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const parseBatchSize = 100

type htmlProspect struct {
	ID          string `json:"id"`
	WebsiteHTML string `json:"website_html"`
}

type parsedWebsite struct {
	PageTitle       string
	MetaDescription string
	H1              string
	BodyText        string
}

// ParseProspects extracts plain HTML metadata for every prospect with saved
// homepage HTML that has not yet been parsed, then analyses parsed prospects.
func ParseProspects(ctx context.Context, cfg Config) error {
	for {
		prospects, err := nextUnparsedProspects(ctx, cfg)
		if err != nil {
			return err
		}
		if len(prospects) == 0 {
			return AnalyseProspects(ctx, cfg)
		}
		failed := 0
		for _, prospect := range prospects {
			parsed, err := parseWebsiteHTML(prospect.WebsiteHTML)
			if err != nil {
				slog.Error("Failed to parse HTML", "prospect_id", prospect.ID, "error", err)
				failed++
				continue
			}
			if err := saveParsedWebsite(ctx, cfg, prospect.ID, parsed); err != nil {
				slog.Error("Failed to save parsed HTML", "prospect_id", prospect.ID, "error", err)
				failed++
				continue
			}
		}
		if failed > 0 {
			return fmt.Errorf("failed to parse or save %d ScreenFizz prospects", failed)
		}
	}
}

func nextUnparsedProspects(ctx context.Context, cfg Config) ([]htmlProspect, error) {
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable)
	query := url.Values{
		"select":       {"id,website_html"},
		"website_html": {"not.is.null"},
		"parsed":       {"eq.false"},
		"order":        {"created_at.asc"},
		"limit":        {fmt.Sprintf("%d", parseBatchSize)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ScreenFizz prospects parse request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("list unparsed ScreenFizz prospects: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read unparsed ScreenFizz prospects response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("list unparsed ScreenFizz prospects: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var prospects []htmlProspect
	if err := json.Unmarshal(body, &prospects); err != nil {
		return nil, fmt.Errorf("decode unparsed ScreenFizz prospects response: %w", err)
	}
	return prospects, nil
}

func parseWebsiteHTML(rawHTML string) (parsedWebsite, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return parsedWebsite{}, fmt.Errorf("parse HTML: %w", err)
	}
	result := parsedWebsite{}
	var body *html.Node
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, inBody bool) {
		if node.Type == html.ElementNode {
			switch strings.ToLower(node.Data) {
			case "title":
				if result.PageTitle == "" {
					result.PageTitle = normalizedNodeText(node)
				}
			case "meta":
				if result.MetaDescription == "" && strings.EqualFold(attribute(node, "name"), "description") {
					result.MetaDescription = normalizeText(attribute(node, "content"))
				}
			case "h1":
				if result.H1 == "" {
					result.H1 = normalizedNodeText(node)
				}
			case "body":
				body = node
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inBody)
		}
	}
	visit(doc, false)
	if body != nil {
		result.BodyText = truncateText(normalizedNodeText(body), 5000)
	}
	return result, nil
}

func attribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func normalizedNodeText(node *html.Node) string {
	parts := make([]string, 0)
	var visit func(*html.Node, bool)
	visit = func(current *html.Node, ignored bool) {
		if current.Type == html.ElementNode {
			switch strings.ToLower(current.Data) {
			case "script", "style", "noscript", "template":
				ignored = true
			}
		}
		if current.Type == html.TextNode && !ignored {
			parts = append(parts, current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child, ignored)
		}
	}
	visit(node, false)
	return normalizeText(strings.Join(parts, " "))
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateText(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func saveParsedWebsite(ctx context.Context, cfg Config, prospectID string, parsed parsedWebsite) error {
	body, err := json.Marshal(map[string]any{
		"page_title":       parsed.PageTitle,
		"meta_description": parsed.MetaDescription,
		"h1":               parsed.H1,
		"body_text":        parsed.BodyText,
		"parsed":           true,
	})
	if err != nil {
		return fmt.Errorf("encode parsed website: %w", err)
	}
	endpoint := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(prospectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz parsed HTML update request: %w", err)
	}
	setSupabaseHeaders(req, cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("save ScreenFizz parsed HTML: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz parsed HTML update response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("save ScreenFizz parsed HTML: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
