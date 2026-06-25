package tel

import (
	"encoding/json"
	"strings"
)

func IsRedsmsConfigured() bool {
	return redsmsClient.client != nil && strings.TrimSpace(redsmsClient.login) != "" && strings.TrimSpace(redsmsClient.apiKey) != ""
}

func InitRedsms(jsonconf json.RawMessage) error {
	return redsmsInit(jsonconf)
}

func SendWaitCall(to, code string) (string, error) {
	return redsmsSendWaitCall(to, code)
}

func SendWaitCallNoCode(to string) (string, string, error) {
	return redsmsSendWaitCallNoCode(to)
}

func CheckWaitCallStatus(uuid string) (string, error) {
	return redsmsCheckMessageStatus(uuid)
}

func SendSmsCode(to, text string) error {
	return redsmsSendWithRoute(to, text, "sms")
}
