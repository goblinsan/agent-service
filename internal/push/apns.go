package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

// APNSConfig describes token-based APNs auth and delivery settings.
type APNSConfig struct {
	Enabled bool
	TeamID  string
	KeyID   string
	AuthKey string // PEM body or path to .p8
	Topic   string
	Env     string // production | sandbox
}

// Dispatcher sends APNs notifications for newly created inbox notifications.
type Dispatcher struct {
	st           store.Store
	cfg          APNSConfig
	client       *http.Client
	privateKey   *ecdsa.PrivateKey
	tokenMu      sync.Mutex
	cachedToken  string
	tokenExpires time.Time
}

func NewAPNSDispatcher(st store.Store, cfg APNSConfig) (*Dispatcher, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(cfg.TeamID) == "" || strings.TrimSpace(cfg.KeyID) == "" || strings.TrimSpace(cfg.Topic) == "" {
		return nil, errors.New("apns requires APNS_TEAM_ID, APNS_KEY_ID, and APNS_TOPIC")
	}
	keyData, err := readAuthKey(cfg.AuthKey)
	if err != nil {
		return nil, fmt.Errorf("read apns auth key: %w", err)
	}
	priv, err := parseECPrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse apns auth key: %w", err)
	}
	env := strings.ToLower(strings.TrimSpace(cfg.Env))
	if env == "" {
		env = "production"
	}
	if env != "production" && env != "sandbox" {
		return nil, fmt.Errorf("invalid APNS_ENV: %q", cfg.Env)
	}
	return &Dispatcher{
		st:         st,
		cfg:        cfg,
		privateKey: priv,
		client: &http.Client{
			Timeout: 8 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
				ForceAttemptHTTP2: true,
			},
		},
	}, nil
}

func (d *Dispatcher) DispatchNotification(ctx context.Context, n store.Notification) error {
	if d == nil || !d.cfg.Enabled || strings.TrimSpace(n.UserID) == "" {
		return nil
	}
	tokens, err := d.st.ListDeviceTokens(ctx, n.UserID, "ios")
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return nil
	}
	payload, err := buildPayload(n)
	if err != nil {
		return err
	}
	jwtToken, err := d.jwt(ctx)
	if err != nil {
		return err
	}
	var failed int
	for _, t := range tokens {
		reason, status, sendErr := d.sendToToken(ctx, t.Token, jwtToken, payload)
		if shouldPruneToken(status, reason) {
			if err := d.st.DeleteDeviceToken(ctx, t.UserID, t.Token); err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.Warn("failed pruning stale device token", "user_id", t.UserID, "error", err)
			}
		}
		if sendErr != nil {
			failed++
			slog.Warn("apns send failed", "user_id", n.UserID, "status", status, "reason", reason, "error", sendErr)
			continue
		}
	}
	if failed > 0 {
		return fmt.Errorf("apns delivery failed for %d/%d tokens", failed, len(tokens))
	}
	return nil
}

func buildPayload(n store.Notification) ([]byte, error) {
	alert := map[string]any{
		"title": n.Title,
		"body":  n.Body,
	}
	aps := map[string]any{
		"alert": alert,
		"sound": "default",
	}
	if n.ThreadID != "" {
		aps["thread-id"] = n.ThreadID
	}
	payload := map[string]any{
		"aps":             aps,
		"notification_id": n.ID,
		"thread_id":       n.ThreadID,
		"kind":            n.Kind,
	}
	if n.Payload != nil {
		payload["data"] = n.Payload
	}
	return json.Marshal(payload)
}

func (d *Dispatcher) sendToToken(ctx context.Context, token, jwtToken string, payload []byte) (string, int, error) {
	url := fmt.Sprintf("%s/3/device/%s", d.baseURL(), strings.TrimSpace(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "bearer "+jwtToken)
	req.Header.Set("apns-topic", d.cfg.Topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "", resp.StatusCode, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	var parsed struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.Reason == "" {
		parsed.Reason = strings.TrimSpace(string(body))
	}
	return parsed.Reason, resp.StatusCode, fmt.Errorf("apns rejected push: status=%d reason=%s", resp.StatusCode, parsed.Reason)
}

func shouldPruneToken(status int, reason string) bool {
	if status == http.StatusGone {
		return true
	}
	r := strings.TrimSpace(reason)
	return status == http.StatusBadRequest && (r == "BadDeviceToken" || r == "Unregistered" || r == "DeviceTokenNotForTopic")
}

func (d *Dispatcher) baseURL() string {
	if strings.ToLower(strings.TrimSpace(d.cfg.Env)) == "sandbox" {
		return "https://api.sandbox.push.apple.com"
	}
	return "https://api.push.apple.com"
}

func (d *Dispatcher) jwt(ctx context.Context) (string, error) {
	now := time.Now().UTC()
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()
	if d.cachedToken != "" && now.Before(d.tokenExpires) {
		return d.cachedToken, nil
	}
	header := map[string]any{"alg": "ES256", "kid": d.cfg.KeyID}
	claims := map[string]any{"iss": d.cfg.TeamID, "iat": now.Unix()}
	headerRaw, _ := json.Marshal(header)
	claimsRaw, _ := json.Marshal(claims)
	unsigned := encodeB64URL(headerRaw) + "." + encodeB64URL(claimsRaw)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := ecdsa.SignASN1(rand.Reader, d.privateKey, sum[:])
	if err != nil {
		return "", err
	}
	token := unsigned + "." + encodeB64URL(sig)
	d.cachedToken = token
	d.tokenExpires = now.Add(50 * time.Minute)
	slog.DebugContext(ctx, "refreshed apns jwt")
	return token, nil
}

func encodeB64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func readAuthKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("empty APNS_AUTH_KEY")
	}
	if strings.Contains(trimmed, "BEGIN PRIVATE KEY") {
		return []byte(trimmed), nil
	}
	if strings.Contains(trimmed, string(os.PathSeparator)) || strings.HasSuffix(strings.ToLower(trimmed), ".p8") {
		abs := trimmed
		if !filepath.IsAbs(abs) {
			cwd, _ := os.Getwd()
			abs = filepath.Join(cwd, trimmed)
		}
		return os.ReadFile(abs)
	}
	return []byte(trimmed), nil
}

func parseECPrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("invalid PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ec, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("APNS auth key is not ECDSA")
		}
		return ec, nil
	}
	if ec, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return ec, nil
	}
	return nil, errors.New("unsupported private key format")
}
