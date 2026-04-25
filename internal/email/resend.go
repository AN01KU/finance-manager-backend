package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Client sends emails via Resend.
// If apiKey is empty, all methods are no-ops (email disabled).
type Client struct {
	apiKey     string
	fromEmail  string
	httpClient *http.Client
}

// New creates a Resend email client.
func New(apiKey, fromEmail string) *Client {
	return &Client{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Enabled reports whether email sending is configured.
func (c *Client) Enabled() bool {
	return c.apiKey != ""
}

type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

type sendResponse struct {
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// Send sends an email. Returns an error if delivery fails.
func (c *Client) Send(to, subject, html string) error {
	if !c.Enabled() {
		log.Printf("[EMAIL] email disabled, would send to=%s subject=%q", to, subject)
		return nil
	}

	req := sendRequest{
		From:    c.fromEmail,
		To:      []string{to},
		Subject: subject,
		HTML:    html,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal email request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create email request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var result sendResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode email response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend API error (status %d): %s", resp.StatusCode, result.Error)
	}

	return nil
}

// SendVerificationCode sends a verification code email.
func (c *Client) SendVerificationCode(to, code string) error {
	html := fmt.Sprintf(`
		<div style="font-family: sans-serif; max-width: 400px; margin: 0 auto; padding: 20px;">
			<h2 style="color: #333;">Verify your email</h2>
			<p style="color: #666;">Enter this code to verify your email address:</p>
			<div style="background: #f4f4f4; border-radius: 8px; padding: 20px; text-align: center; margin: 20px 0;">
				<span style="font-size: 32px; font-weight: bold; letter-spacing: 8px; color: #333;">%s</span>
			</div>
			<p style="color: #999; font-size: 13px;">This code expires in 10 minutes. If you didn't create an account, you can ignore this email.</p>
		</div>
	`, code)

	return c.Send(to, "Verify your email", html)
}
