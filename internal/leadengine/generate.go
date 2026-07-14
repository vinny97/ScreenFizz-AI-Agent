package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const leadStatusEmailReady = "EMAIL_READY"

// GenerateResult summarizes an email generation run.
type GenerateResult struct {
	Generated int
}

type generationLead struct {
	ID          json.RawMessage `json:"id"`
	FirstName   string          `json:"first_name"`
	LastName    string          `json:"last_name"`
	Email       string          `json:"email"`
	Company     string          `json:"company"`
	Website     string          `json:"website"`
	LinkedIn    string          `json:"linkedin_url"`
	JobTitle    string          `json:"job_title"`
	Industry    string          `json:"industry"`
	CompanySize string          `json:"company_size"`
	Country     string          `json:"country"`
}

type activeEmailCampaign struct {
	Subject       string
	HTMLTemplate  string
	TextTemplate  string
	HTMLSignature string
	TextSignature string
	SenderName    string
	SenderEmail   string
	CTAText       string
	CTAURL        string
}

var (
	placeholderPattern = regexp.MustCompile(`{{\s*([a-zA-Z0-9_]+)\s*}}`)
	emDashPattern      = regexp.MustCompile(`\s*—\s*`)
)

// GenerateQueuedEmails copies the active campaign email template into queued leads.
func (c *Client) GenerateQueuedEmails(ctx context.Context) (GenerateResult, error) {
	campaign, err := c.getActiveEmailCampaign(ctx)
	if err != nil {
		return GenerateResult{}, err
	}
	leads, err := c.listQueuedLeads(ctx)
	if err != nil {
		return GenerateResult{}, err
	}

	result := GenerateResult{}
	for _, lead := range leads {
		htmlBody := renderTemplateWithSignature(campaign.HTMLTemplate, campaign.HTMLSignature, buildPlaceholderValues(lead, campaign))
		textBody := renderTemplateWithSignature(campaign.TextTemplate, campaign.TextSignature, buildPlaceholderValues(lead, campaign))
		htmlBody = removeEmailEmDashes(htmlBody)
		textBody = removeEmailEmDashes(textBody)
		if err := c.updateLeadEmailContent(ctx, lead.ID, campaign.Subject, htmlBody, textBody); err != nil {
			return GenerateResult{}, err
		}
		result.Generated++
	}
	return result, nil
}

func (c *Client) getActiveEmailCampaign(ctx context.Context) (*activeEmailCampaign, error) {
	campaigns, err := c.ListCampaigns(ctx)
	if err != nil {
		return nil, err
	}

	var active Campaign
	for _, campaign := range campaigns {
		if !campaignIsActive(campaign) {
			continue
		}
		if active != nil {
			return nil, errors.New("multiple active campaigns found")
		}
		active = campaign
	}
	if active == nil {
		return nil, errors.New("no active campaign found")
	}

	subject, ok := active["email_subject"].(string)
	if !ok {
		return nil, errors.New("active campaign has no email_subject")
	}
	htmlTemplate, ok := active["email_template_html"].(string)
	if !ok {
		return nil, errors.New("active campaign has no email_template_html")
	}
	textTemplate, ok := active["email_template_text"].(string)
	if !ok {
		return nil, errors.New("active campaign has no email_template_text")
	}
	htmlSignature, ok := active["email_signature_html"].(string)
	if !ok {
		return nil, errors.New("active campaign has no email_signature_html")
	}
	textSignature, ok := active["email_signature_text"].(string)
	if !ok {
		return nil, errors.New("active campaign has no email_signature_text")
	}
	senderName, ok := active["sender_name"].(string)
	if !ok {
		return nil, errors.New("active campaign has no sender_name")
	}
	senderEmail, ok := active["sender_email"].(string)
	if !ok {
		return nil, errors.New("active campaign has no sender_email")
	}
	ctaText, ok := active["cta_text"].(string)
	if !ok {
		return nil, errors.New("active campaign has no cta_text")
	}
	ctaURL, ok := active["cta_url"].(string)
	if !ok {
		return nil, errors.New("active campaign has no cta_url")
	}
	return &activeEmailCampaign{
		Subject:       subject,
		HTMLTemplate:  htmlTemplate,
		TextTemplate:  textTemplate,
		HTMLSignature: htmlSignature,
		TextSignature: textSignature,
		SenderName:    senderName,
		SenderEmail:   senderEmail,
		CTAText:       ctaText,
		CTAURL:        ctaURL,
	}, nil
}

func (c *Client) listQueuedLeads(ctx context.Context) ([]generationLead, error) {
	leads := make([]generationLead, 0)
	for start := 0; ; {
		query := url.Values{}
		query.Set("select", "id,first_name,last_name,email,company,website,linkedin_url,job_title,industry,company_size,country")
		query.Set("status", "eq."+leadStatusQueued)
		requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create queued leads request: %w", err)
		}
		c.setSupabaseHeaders(req)
		req.Header.Set("Prefer", "count=exact")
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+pageSize-1))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list queued leads: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read queued leads: %w", err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return nil, fmt.Errorf("list queued leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var page []generationLead
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode queued leads: %w", err)
		}
		leads = append(leads, page...)
		total, hasTotal := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (hasTotal && len(leads) >= total) || (!hasTotal && len(page) < pageSize) {
			return leads, nil
		}
		start += len(page)
	}
}

func (c *Client) updateLeadEmailContent(ctx context.Context, rawID json.RawMessage, subject, htmlBody, textBody string) error {
	id, err := qualificationLeadID(rawID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{
		"email_subject":   subject,
		"email_body_html": htmlBody,
		"email_body_text": textBody,
		"status":          leadStatusEmailReady,
	})
	if err != nil {
		return fmt.Errorf("encode lead email content: %w", err)
	}
	query := url.Values{}
	query.Set("id", "eq."+id)
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create lead email content request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update lead email content: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read lead email content response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update lead email content: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func buildPlaceholderValues(lead generationLead, campaign *activeEmailCampaign) map[string]string {
	fullName := strings.TrimSpace(strings.TrimSpace(lead.FirstName) + " " + strings.TrimSpace(lead.LastName))
	return map[string]string{
		"first_name":      lead.FirstName,
		"last_name":       lead.LastName,
		"full_name":       fullName,
		"email":           lead.Email,
		"company":         lead.Company,
		"website":         lead.Website,
		"linkedin":        lead.LinkedIn,
		"job_title":       lead.JobTitle,
		"industry":        lead.Industry,
		"company_size":    lead.CompanySize,
		"country":         lead.Country,
		"sender_name":     campaign.SenderName,
		"sender_email":    campaign.SenderEmail,
		"cta_text":        campaign.CTAText,
		"cta_url":         campaign.CTAURL,
		"unsubscribe_url": "",
	}
}

func renderTemplateWithSignature(template, signature string, placeholders map[string]string) string {
	renderedSignature := renderPlaceholders(signature, placeholders)
	placeholdersWithSignature := clonePlaceholders(placeholders)
	placeholdersWithSignature["signature"] = renderedSignature
	rendered := renderPlaceholders(template, placeholdersWithSignature)
	if strings.Contains(template, "{{signature}}") || strings.TrimSpace(signature) == "" {
		return rendered
	}
	if strings.TrimSpace(rendered) == "" {
		return renderedSignature
	}
	return rendered + "\n\n" + renderedSignature
}

func renderPlaceholders(template string, placeholders map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		parts := placeholderPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		if value, ok := placeholders[parts[1]]; ok {
			return value
		}
		return ""
	})
}

func removeEmailEmDashes(value string) string {
	return emDashPattern.ReplaceAllString(value, ", ")
}

func clonePlaceholders(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
