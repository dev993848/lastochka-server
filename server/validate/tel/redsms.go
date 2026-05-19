package tel

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type redsmsConfig struct {
	Login      string `json:"login"`
	APIKey     string `json:"api_key"`
	Route      string `json:"route"`
	BaseURL    string `json:"base_url"`
	SenderName string `json:"sender_name"`
}

type redsmsMessageRequest struct {
	Route string `json:"route"`
	From  string `json:"from,omitempty"`
	To    string `json:"to"`
	Text  string `json:"text"`
}

type redsmsMessageResponse struct {
	Success bool `json:"success"`
	Count   int  `json:"count"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

var redsmsClient struct {
	login      string
	apiKey     string
	route      string
	baseURL    string
	senderName string
	client     *http.Client
}

func redsmsInit(jsonconf json.RawMessage) error {
	var conf redsmsConfig
	if err := json.Unmarshal(jsonconf, &conf); err != nil {
		return err
	}

	conf.Login = strings.TrimSpace(conf.Login)
	conf.APIKey = strings.TrimSpace(conf.APIKey)
	if conf.Login == "" || conf.APIKey == "" {
		return errors.New("redsms_conf.login and redsms_conf.api_key are required")
	}

	conf.Route = strings.TrimSpace(conf.Route)
	if conf.Route == "" {
		conf.Route = "sms"
	}

	conf.BaseURL = strings.TrimSpace(conf.BaseURL)
	if conf.BaseURL == "" {
		conf.BaseURL = "https://cp.redsms.ru/api/message"
	}

	redsmsClient.login = conf.Login
	redsmsClient.apiKey = conf.APIKey
	redsmsClient.route = conf.Route
	redsmsClient.baseURL = conf.BaseURL
	redsmsClient.senderName = strings.TrimSpace(conf.SenderName)
	redsmsClient.client = &http.Client{Timeout: 10 * time.Second}

	return nil
}

func redsmsSend(to, code string) error {
	ts := fmt.Sprintf("ts-%d", time.Now().UnixMilli())
	sum := md5.Sum([]byte(ts + redsmsClient.apiKey))
	secret := hex.EncodeToString(sum[:])

	reqBody := redsmsMessageRequest{
		Route: redsmsClient.route,
		From:  redsmsClient.senderName,
		To:    to,
		Text:  code,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, redsmsClient.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("login", redsmsClient.login)
	req.Header.Set("ts", ts)
	req.Header.Set("secret", secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := redsmsClient.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("redsms returned status %d", resp.StatusCode)
	}

	var data redsmsMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}
	if !data.Success {
		if len(data.Errors) > 0 {
			return fmt.Errorf("redsms error %d: %s", data.Errors[0].Code, data.Errors[0].Message)
		}
		return errors.New("redsms request failed")
	}

	return nil
}
