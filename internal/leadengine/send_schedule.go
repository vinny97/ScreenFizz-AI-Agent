package leadengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	workingDayStartHour = 9
	workingDayEndHour   = 16
	defaultHourlyLimit  = 72
)

// SendSchedule controls production email pacing for the campaign scheduler.
type SendSchedule struct {
	DailyLimit     int
	HourlyLimit    int
	StartHour      int
	EndHour        int
	MinDelay       time.Duration
	MaxDelay       time.Duration
	MinHourlyPause time.Duration
	MaxHourlyPause time.Duration
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error
	RandomDuration func(time.Duration, time.Duration) time.Duration
}

func DefaultSendSchedule() SendSchedule {
	return SendSchedule{
		DailyLimit:     500,
		HourlyLimit:    defaultHourlyLimit,
		StartHour:      workingDayStartHour,
		EndHour:        workingDayEndHour,
		MinDelay:       20 * time.Second,
		MaxDelay:       60 * time.Second,
		MinHourlyPause: 2 * time.Minute,
		MaxHourlyPause: 5 * time.Minute,
		Now:            time.Now,
		Sleep:          sleepContext,
		RandomDuration: randomDuration,
	}
}

// SendReadyLeadsForCampaignPaced sends stored campaign emails sequentially
// within the configured weekday working window. Limits count recipient messages,
// not lead rows, so a lead with two addresses consumes two send slots.
func (c *Client) SendReadyLeadsForCampaignPaced(ctx context.Context, sender *TestSender, campaign ScheduledCampaign, schedule SendSchedule) (SendResult, error) {
	if sender == nil {
		return SendResult{}, errors.New("Brevo sender is required")
	}
	location, err := time.LoadLocation(strings.TrimSpace(campaign.Timezone))
	if err != nil {
		return SendResult{}, fmt.Errorf("load campaign timezone: %w", err)
	}
	schedule = normalizeSendSchedule(schedule, campaign.SendLimit())
	localNow := schedule.Now().In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	alreadySent, err := c.countSentRecipientsSince(ctx, campaign.ID, dayStart)
	if err != nil {
		return SendResult{}, err
	}
	schedule.DailyLimit -= alreadySent
	if schedule.DailyLimit <= 0 {
		return SendResult{}, nil
	}
	leads, err := c.listEmailReadyLeadsByCampaign(ctx, campaign.Name, schedule.DailyLimit)
	if err != nil {
		return SendResult{}, err
	}
	activeSender := &activeSenderCampaign{SenderName: campaign.SenderName, SenderEmail: campaign.SenderEmail, ReplyDomain: campaign.ReplyDomain}
	result := SendResult{}
	hourSent := 0
	currentHour := ""
	pausedHours := make(map[string]struct{})
	needsDelay := false

	for _, lead := range leads {
		recipients, err := c.ensureLeadRecipients(ctx, campaign.ID, lead)
		if err != nil {
			return result, err
		}
		if len(recipients) == 0 {
			result.Failed++
			continue
		}
		eligible := make([]emailRecipient, 0, len(recipients))
		for _, recipient := range recipients {
			if recipient.Status == "SENT" || recipient.Status == "DELIVERED" || recipient.Status == "DEFERRED" || recipient.Status == "HARD_BOUNCED" || recipient.Status == "INVALID" || recipient.Status == "BLOCKED" || recipient.Status == "REPLIED" || recipient.Status == "UNSUBSCRIBED" || recipient.Status == "FAILED_PERMANENT" {
				continue
			}
			if recipient.NextRetryAt != nil && recipient.NextRetryAt.After(schedule.Now()) {
				continue
			}
			suppressed, err := c.recipientSuppressed(ctx, recipient.Email)
			if err != nil {
				return result, err
			}
			if suppressed {
				_ = c.updateRecipient(ctx, recipient.ID, map[string]any{"status": "FAILED_PERMANENT", "last_error": "recipient suppressed"})
				continue
			}
			eligible = append(eligible, recipient)
		}
		if result.MessagesSent+len(eligible) > schedule.DailyLimit {
			continue
		}
		if len(eligible) == 0 {
			continue
		}

		messageIDs := make([]string, 0, len(eligible))
		leadFailed := false
		for _, recipient := range eligible {
			for {
				now := schedule.Now().In(location)
				if !insideSendWindow(now, schedule.StartHour, schedule.EndHour) {
					slog.Info("leadengine.send.window_closed", "campaign", campaign.Name, "messages_sent", result.MessagesSent)
					return result, nil
				}
				hourKey := now.Format("2006-01-02-15")
				if hourKey != currentHour {
					currentHour = hourKey
					hourSent = 0
				}
				if _, paused := pausedHours[hourKey]; !paused {
					pause := schedule.RandomDuration(schedule.MinHourlyPause, schedule.MaxHourlyPause)
					slog.Info("leadengine.send.hourly_pause", "campaign", campaign.Name, "duration", pause.String())
					if err := schedule.Sleep(ctx, pause); err != nil {
						return result, err
					}
					pausedHours[hourKey] = struct{}{}
					continue
				}
				if hourSent >= schedule.HourlyLimit {
					nextHour := now.Truncate(time.Hour).Add(time.Hour)
					if err := schedule.Sleep(ctx, nextHour.Sub(now)); err != nil {
						return result, err
					}
					continue
				}
				if needsDelay {
					delay := schedule.RandomDuration(schedule.MinDelay, schedule.MaxDelay)
					if err := schedule.Sleep(ctx, delay); err != nil {
						return result, err
					}
					needsDelay = false
					continue
				}
				break
			}

			replyTo := ""
			if strings.TrimSpace(campaign.ReplyDomain) != "" && recipient.ReplyToken != "" {
				replyTo = "reply+" + recipient.ReplyToken + "@" + strings.TrimSpace(campaign.ReplyDomain)
			}
			messageID, sendErr := sendBrevoWithRetry(ctx, sender, activeSender, &lead, recipient.Email, replyTo, schedule)
			if sendErr != nil {
				result.Failed++
				leadFailed = true
				status := "FAILED_PERMANENT"
				fields := map[string]any{"status": status, "attempt_count": recipient.AttemptCount + 1, "last_attempt_at": schedule.Now().UTC().Format(time.RFC3339), "last_error": sendErr.Error()}
				if retryableBrevoError(sendErr) {
					fields["status"] = "FAILED_RETRYABLE"
					fields["next_retry_at"] = schedule.Now().Add(time.Hour).UTC().Format(time.RFC3339)
				}
				if err := c.updateRecipient(ctx, recipient.ID, fields); err != nil {
					return result, err
				}
				slog.Error("leadengine.send.failed", "error", sendErr, "lead_id", string(lead.ID), "email", recipient.Email)
				break
			}
			if err := c.updateRecipient(ctx, recipient.ID, map[string]any{
				"status": "SENT", "brevo_message_id": messageID, "attempt_count": recipient.AttemptCount + 1,
				"last_attempt_at": schedule.Now().UTC().Format(time.RFC3339), "sent_at": schedule.Now().UTC().Format(time.RFC3339),
				"next_retry_at": nil, "last_error": nil,
			}); err != nil {
				return result, err
			}
			messageIDs = append(messageIDs, messageID)
			result.MessagesSent++
			hourSent++
			needsDelay = true
			paused, err := c.campaignPaused(ctx, campaign.ID)
			if err != nil {
				return result, err
			}
			if paused {
				slog.Warn("leadengine.send.campaign_paused", "campaign", campaign.Name, "messages_sent", result.MessagesSent)
				return result, nil
			}
		}
		if leadFailed {
			continue
		}
		if err := c.markLeadSent(ctx, lead.ID, strings.Join(messageIDs, ",")); err != nil {
			return result, err
		}
		result.Sent++
		if result.MessagesSent >= schedule.DailyLimit {
			break
		}
	}
	return result, nil
}

func (c *Client) campaignPaused(ctx context.Context, campaignID string) (bool, error) {
	var rows []struct {
		PausedAt *time.Time `json:"paused_at"`
	}
	if err := c.restJSON(ctx, http.MethodGet, "campaigns", url.Values{"select": {"paused_at"}, "id": {"eq." + campaignID}}, nil, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].PausedAt != nil, nil
}

func sendBrevoWithRetry(ctx context.Context, sender *TestSender, campaign *activeSenderCampaign, lead *emailReadyLead, email, replyTo string, schedule SendSchedule) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		messageID, err := sender.send(ctx, campaign, lead, email, replyTo)
		if err == nil {
			return messageID, nil
		}
		lastErr = err
		if !retryableBrevoError(err) || attempt == 3 {
			break
		}
		delay := time.Duration(1<<attempt) * time.Second
		var apiErr *BrevoAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
			if seconds, parseErr := strconv.Atoi(apiErr.Header.Get("x-sib-ratelimit-reset")); parseErr == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			} else if seconds, parseErr := strconv.Atoi(apiErr.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			}
		}
		if err := schedule.Sleep(ctx, delay); err != nil {
			return "", err
		}
	}
	return "", lastErr
}

func retryableBrevoError(err error) bool {
	var apiErr *BrevoAPIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
}

func normalizeSendSchedule(schedule SendSchedule, campaignLimit int) SendSchedule {
	defaults := DefaultSendSchedule()
	if schedule.DailyLimit <= 0 || schedule.DailyLimit > campaignLimit {
		schedule.DailyLimit = campaignLimit
	}
	if schedule.HourlyLimit <= 0 {
		schedule.HourlyLimit = defaults.HourlyLimit
	}
	if schedule.EndHour <= schedule.StartHour {
		schedule.StartHour, schedule.EndHour = defaults.StartHour, defaults.EndHour
	}
	if schedule.Now == nil {
		schedule.Now = defaults.Now
	}
	if schedule.Sleep == nil {
		schedule.Sleep = defaults.Sleep
	}
	if schedule.RandomDuration == nil {
		schedule.RandomDuration = defaults.RandomDuration
	}
	return schedule
}

func insideSendWindow(now time.Time, startHour, endHour int) bool {
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}
	return now.Hour() >= startHour && now.Hour() < endHour
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomDuration(minimum, maximum time.Duration) time.Duration {
	if maximum <= minimum {
		return minimum
	}
	return minimum + time.Duration(rand.Int63n(int64(maximum-minimum)+1))
}
