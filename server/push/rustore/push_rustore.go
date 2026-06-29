// Package rustore implements push notification plugin for RuStore Push (RPS).
// RuStore Push — сервис push-уведомлений от VK, работающий без Google Play Services.
// https://help.rustore.ru/developers/services/Push
package rustore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/push/common"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

var errInvalidToken = errors.New("invalid push token")
var errTransient = errors.New("transient error")

var handler Handler

const (
	bufferSize   = 1024
	pushBaseURL  = "https://push-api.rustore.ru"
	pushEndpoint = "/v1/messages:send"
	httpTimeout  = 10 * time.Second
)

type Handler struct {
	input  chan *push.Receipt
	stop   chan bool
	apiKey string
	projectID string
	client *http.Client
	config *configType
}

type configType struct {
	Enabled     bool           `json:"enabled"`
	ProjectID   string         `json:"project_id"`
	APIKey      string         `json:"api_key"`
	APIKeyFile  string         `json:"api_key_file"`
	BaseURL     string         `json:"base_url,omitempty"`
	Android     *common.Config `json:"android,omitempty"`
}

type pushRequest struct {
	Token        string            `json:"token"`
	ProjectID    string            `json:"project_id"`
	Notification *pushNotification `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
}

type pushNotification struct {
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	ClickAction string `json:"click_action,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	Sound       string `json:"sound,omitempty"`
}

type pushErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (Handler) Init(jsonconf json.RawMessage) (bool, error) {
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return false, errors.New("failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return false, nil
	}

	if config.ProjectID == "" {
		return false, errors.New("missing project_id")
	}

	apiKey := config.APIKey
	if apiKey == "" && config.APIKeyFile != "" {
		keyBytes, err := os.ReadFile(config.APIKeyFile)
		if err != nil {
			return false, fmt.Errorf("failed to read api_key_file: %v", err)
		}
		apiKey = strings.TrimSpace(string(keyBytes))
	}
	if apiKey == "" {
		return false, errors.New("missing api_key or api_key_file")
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = pushBaseURL
	}

	handler.apiKey = apiKey
	handler.projectID = config.ProjectID
	handler.config = &config
	handler.input = make(chan *push.Receipt, bufferSize)
	handler.stop = make(chan bool, 1)
	handler.client = &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	go func() {
		for {
			select {
			case rcpt := <-handler.input:
				go handler.sendPush(rcpt)
			case <-handler.stop:
				return
			}
		}
	}()

	logs.Info.Println("rustore push handler initialized, project:", config.ProjectID)
	return true, nil
}

func (Handler) IsReady() bool {
	return handler.input != nil
}

func (Handler) Push() chan<- *push.Receipt {
	return handler.input
}

func (Handler) Channel() chan<- *push.ChannelReq {
	return nil
}

func (Handler) Stop() {
	handler.stop <- true
}

type deviceRequest struct {
	uid  types.Uid
	req  pushRequest
}

func (h *Handler) sendPush(rcpt *push.Receipt) {
	uids := make([]types.Uid, 0, len(rcpt.To))
	for uid := range rcpt.To {
		uids = append(uids, uid)
	}

	devices, count, err := store.Devices.GetAll(uids...)
	if err != nil {
		logs.Warn.Println("rustore: failed to get devices:", err)
		return
	}
	if count == 0 {
		return
	}

	for uid, devList := range devices {
		recipient := rcpt.To[uid]
		for _, dev := range devList {
			if dev.DeviceId == "" {
				continue
			}

			title := h.config.GetStringField(rcpt.Payload.What, "Title")
			if title == "" {
				title = "Ласточка"
			}

			body := h.config.GetStringField(rcpt.Payload.What, "Body")
			if body == "" {
				body = "Новое сообщение"
			}

			if rcpt.Payload.What == push.ActMsg && rcpt.Payload.Content != nil {
				if contentStr, ok := rcpt.Payload.Content.(string); ok && contentStr != "" {
					body = contentStr
				}
			}

			data := map[string]string{
				"topic":  rcpt.Payload.Topic,
				"what":   rcpt.Payload.What,
				"silent": fmt.Sprintf("%v", rcpt.Payload.Silent),
			}
			if rcpt.Payload.SeqId > 0 {
				data["seq"] = fmt.Sprintf("%d", rcpt.Payload.SeqId)
			}
			if rcpt.Payload.From != "" {
				data["from"] = rcpt.Payload.From
			}
			if recipient.Unread > 0 {
				data["unread"] = fmt.Sprintf("%d", recipient.Unread)
			}

			dr := deviceRequest{
				uid: uid,
				req: pushRequest{
					Token:     dev.DeviceId,
					ProjectID: h.projectID,
					Notification: &pushNotification{
						Title:       title,
						Body:        body,
						ClickAction: h.config.GetStringField(rcpt.Payload.What, "ClickAction"),
						Icon:        h.config.GetStringField(rcpt.Payload.What, "Icon"),
						Color:       h.config.GetStringField(rcpt.Payload.What, "Color"),
						Sound:       h.config.GetStringField(rcpt.Payload.What, "Sound"),
					},
					Data: data,
				},
			}

			if err := h.sendRequest(&dr); err != nil {
				logs.Warn.Println("rustore: failed to send push:", err)
			}
		}
	}
}

func (h *Handler) sendRequest(dr *deviceRequest) error {
	body, err := json.Marshal(&dr.req)
	if err != nil {
		return fmt.Errorf("marshal error: %v", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, pushBaseURL+pushEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request build error: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("Authorization", "Bearer "+h.apiKey)

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http error: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var errResp pushErrorResponse
	if json.Unmarshal(respBody, &errResp) == nil {
		switch errResp.Code {
		case 410, 404:
			logs.Warn.Println("rustore: invalid token for uid", dr.uid.UserId())
			if err := store.Devices.Delete(dr.uid, dr.req.Token); err != nil {
				logs.Warn.Println("rustore: failed to delete invalid token:", err)
			}
			return errInvalidToken
		case 429:
			return errTransient
		}
		return fmt.Errorf("api error: code=%d, message=%s", errResp.Code, errResp.Message)
	}

	if resp.StatusCode >= 500 {
		return errTransient
	}

	return fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
}

func init() {
	push.Register("rustore", &handler)
}
