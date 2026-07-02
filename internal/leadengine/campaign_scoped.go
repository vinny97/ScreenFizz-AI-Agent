package leadengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) QualifyNewLeadsForCampaign(ctx context.Context, campaignName string) (QualificationResult, error) {
	leads, err := c.listNewLeadsByCampaign(ctx, campaignName)
	if err != nil {
		return QualificationResult{}, err
	}
	result := QualificationResult{}
	for _, lead := range leads {
		status := leadStatusReadyToEmail
		if shouldRejectLead(lead.Email, lead.Website) {
			status = leadStatusRejected
		}
		if err := c.updateLeadStatus(ctx, lead.ID, status); err != nil {
			return QualificationResult{}, err
		}
		if status == leadStatusReadyToEmail {
			result.Qualified++
		} else {
			result.Rejected++
		}
	}
	return result, nil
}

func (c *Client) QueueReadyLeadsForCampaign(ctx context.Context, campaignName string, limit int) (QueueResult, error) {
	leads, err := c.listLeadsByCampaignAndStatus(ctx, campaignName, leadStatusReadyToEmail, limit, "id")
	if err != nil {
		return QueueResult{}, err
	}
	result := QueueResult{}
	for _, lead := range leads {
		if err := c.updateLeadStatus(ctx, lead.ID, leadStatusQueued); err != nil {
			return QueueResult{}, err
		}
		result.Queued++
	}
	return result, nil
}

func (c *Client) GenerateQueuedEmailsForCampaign(ctx context.Context, campaign ScheduledCampaign) (GenerateResult, error) {
	leads, err := c.listGenerationLeadsByCampaignAndStatus(ctx, campaign.Name, leadStatusQueued)
	if err != nil {
		return GenerateResult{}, err
	}
	active := &activeEmailCampaign{
		Subject:       campaign.EmailSubject,
		HTMLTemplate:  campaign.EmailTemplateHTML,
		TextTemplate:  campaign.EmailTemplateText,
		HTMLSignature: campaign.EmailSignatureHTML,
		TextSignature: campaign.EmailSignatureText,
		SenderName:    campaign.SenderName,
		SenderEmail:   campaign.SenderEmail,
		CTAText:       campaign.CTAText,
		CTAURL:        campaign.CTAURL,
	}
	result := GenerateResult{}
	for _, lead := range leads {
		htmlBody := renderTemplateWithSignature(active.HTMLTemplate, active.HTMLSignature, buildPlaceholderValues(lead, active))
		textBody := renderTemplateWithSignature(active.TextTemplate, active.TextSignature, buildPlaceholderValues(lead, active))
		if err := c.updateLeadEmailContent(ctx, lead.ID, active.Subject, htmlBody, textBody); err != nil {
			return GenerateResult{}, err
		}
		result.Generated++
	}
	return result, nil
}

func (c *Client) SendReadyLeadsForCampaign(ctx context.Context, sender *TestSender, campaign ScheduledCampaign, limit int) (SendResult, error) {
	if sender == nil {
		return SendResult{}, errors.New("Brevo sender is required")
	}
	activeSender := &activeSenderCampaign{
		SenderName:  campaign.SenderName,
		SenderEmail: campaign.SenderEmail,
	}
	leads, err := c.listEmailReadyLeadsByCampaign(ctx, campaign.Name, limit)
	if err != nil {
		return SendResult{}, err
	}
	result := SendResult{}
	for _, lead := range leads {
		recipients := splitLeadEmails(lead.Email)
		if len(recipients) == 0 {
			result.Failed++
			continue
		}
		messageIDs, sendErr := sender.sendLeadEmails(ctx, activeSender, &lead, recipients)
		if sendErr != nil {
			result.Failed++
			slog.Error("leadengine.send.failed", "error", sendErr, "lead_id", string(lead.ID), "email", lead.Email)
			continue
		}
		if err := c.markLeadSent(ctx, lead.ID, strings.Join(messageIDs, ",")); err != nil {
			return SendResult{}, err
		}
		result.Sent++
	}
	return result, nil
}

func (c *Client) listNewLeadsByCampaign(ctx context.Context, campaignName string) ([]qualificationLead, error) {
	query := url.Values{}
	query.Set("select", "id,email,website")
	query.Set("status", "eq."+leadStatusNew)
	query.Set("campaign", "eq."+campaignName)
	return c.listQualificationLeads(ctx, query, "list new leads")
}

func (c *Client) listQualificationLeads(ctx context.Context, query url.Values, action string) ([]qualificationLead, error) {
	leads := make([]qualificationLead, 0)
	for start := 0; ; {
		requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create %s request: %w", action, err)
		}
		c.setSupabaseHeaders(req)
		req.Header.Set("Prefer", "count=exact")
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+pageSize-1))
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", action, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read %s: %w", action, err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return nil, fmt.Errorf("%s: Supabase returned %s: %s", action, resp.Status, strings.TrimSpace(string(body)))
		}
		var page []qualificationLead
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode %s: %w", action, err)
		}
		leads = append(leads, page...)
		total, hasTotal := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (hasTotal && len(leads) >= total) || (!hasTotal && len(page) < pageSize) {
			return leads, nil
		}
		start += len(page)
	}
}

func (c *Client) listLeadsByCampaignAndStatus(ctx context.Context, campaignName, status string, limit int, selectFields string) ([]qualificationLead, error) {
	query := url.Values{}
	query.Set("select", selectFields)
	query.Set("status", "eq."+status)
	query.Set("campaign", "eq."+campaignName)
	query.Set("order", "created_at.asc")
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create campaign leads request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", fmt.Sprintf("0-%d", max(limit, 1)-1))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list campaign leads: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read campaign leads: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("list campaign leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var leads []qualificationLead
	if err := json.Unmarshal(body, &leads); err != nil {
		return nil, fmt.Errorf("decode campaign leads: %w", err)
	}
	return leads, nil
}

func (c *Client) listGenerationLeadsByCampaignAndStatus(ctx context.Context, campaignName, status string) ([]generationLead, error) {
	leads := make([]generationLead, 0)
	for start := 0; ; {
		query := url.Values{}
		query.Set("select", "id,first_name,last_name,email,company,website,linkedin_url,job_title,industry,company_size,country")
		query.Set("status", "eq."+status)
		query.Set("campaign", "eq."+campaignName)
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

func (c *Client) listEmailReadyLeadsByCampaign(ctx context.Context, campaignName string, limit int) ([]emailReadyLead, error) {
	query := url.Values{}
	query.Set("select", "id,email,email_subject,email_body_html,email_body_text")
	query.Set("status", "eq."+leadStatusEmailReady)
	query.Set("campaign", "eq."+campaignName)
	query.Set("order", "created_at.asc")
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create EMAIL_READY leads request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", fmt.Sprintf("0-%d", max(limit, 1)-1))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list EMAIL_READY leads: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read EMAIL_READY leads: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("list EMAIL_READY leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var leads []emailReadyLead
	if err := json.Unmarshal(body, &leads); err != nil {
		return nil, fmt.Errorf("decode EMAIL_READY leads: %w", err)
	}
	return leads, nil
}
