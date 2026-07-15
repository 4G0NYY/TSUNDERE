package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Notifier delivers alerts via Discord webhook and/or SMTP mail. All of its
// configuration lives in the settings table so it can be changed from the
// admin UI without a restart.
type Notifier struct {
	store *Store
}

func NewNotifier(store *Store) *Notifier {
	return &Notifier{store: store}
}

// NotifyStatusChange sends the down/up alert for a monitor transition.
func (n *Notifier) NotifyStatusChange(m Monitor, hb Heartbeat) {
	var title, body string
	var color int
	if hb.Status == StatusDown {
		title = fmt.Sprintf("🔻 %s is DOWN", m.Name)
		body = fmt.Sprintf("H-hey!! **%s** just went down! %s\nIt's not like I'm worried or anything... but you should probably fix it. Now. Baka.", m.Name, detail(hb))
		color = 0xe45c5c
	} else {
		title = fmt.Sprintf("💚 %s is UP", m.Name)
		body = fmt.Sprintf("**%s** is back up. %s\nHmph. Took you long enough. (I-I wasn't worried. Really.)", m.Name, detail(hb))
		color = 0x5ce49a
	}
	n.dispatch(title, body, color)
}

func detail(hb Heartbeat) string {
	parts := []string{}
	if hb.Message != "" {
		parts = append(parts, hb.Message)
	}
	if hb.Status == StatusUp && hb.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("%.0f ms", hb.LatencyMS))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (n *Notifier) dispatch(title, body string, color int) {
	if webhook := n.store.GetSetting("discord_webhook_url"); webhook != "" {
		if err := n.SendDiscord(webhook, title, body, color); err != nil {
			log.Printf("alert: discord delivery failed: %v", err)
		}
	}
	if n.store.GetSetting("smtp_host") != "" && n.store.GetSetting("smtp_to") != "" {
		if err := n.SendMail(title, strings.ReplaceAll(body, "**", "")); err != nil {
			log.Printf("alert: mail delivery failed: %v", err)
		}
	}
}

func (n *Notifier) SendDiscord(webhook, title, body string, color int) error {
	payload := map[string]any{
		"username": "TSUNDERE",
		"embeds": []map[string]any{{
			"title":       title,
			"description": body,
			"color":       color,
			"footer":      map[string]any{"text": "TSUNDERE monitoring"},
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}},
	}
	b, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// SendMail sends a plain-text alert mail using the SMTP settings from the DB.
// Port 465 gets implicit TLS; anything else uses plain/STARTTLS via net/smtp.
func (n *Notifier) SendMail(subject, body string) error {
	host := n.store.GetSetting("smtp_host")
	port := n.store.GetSetting("smtp_port")
	if port == "" {
		port = "587"
	}
	user := n.store.GetSetting("smtp_user")
	pass := n.store.GetSetting("smtp_pass")
	from := n.store.GetSetting("smtp_from")
	if from == "" {
		from = user
	}
	var to []string
	for _, t := range strings.Split(n.store.GetSetting("smtp_to"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			to = append(to, t)
		}
	}
	if host == "" || from == "" || len(to) == 0 {
		return fmt.Errorf("smtp not fully configured (need host, from/user and recipients)")
	}

	msg := []byte(fmt.Sprintf("From: TSUNDERE <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, strings.Join(to, ", "), subject, body))

	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}

	if port == "465" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
		if err := c.Mail(from); err != nil {
			return err
		}
		for _, rcpt := range to {
			if err := c.Rcpt(rcpt); err != nil {
				return err
			}
		}
		wc, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := wc.Write(msg); err != nil {
			return err
		}
		if err := wc.Close(); err != nil {
			return err
		}
		return c.Quit()
	}

	// net/smtp upgrades to STARTTLS automatically when the server offers it.
	return smtp.SendMail(addr, auth, from, to, msg)
}
