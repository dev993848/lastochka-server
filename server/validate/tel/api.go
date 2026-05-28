package tel

import "strings"

func IsRedsmsConfigured() bool {
	return redsmsClient.client != nil && strings.TrimSpace(redsmsClient.login) != "" && strings.TrimSpace(redsmsClient.apiKey) != ""
}

func SendWaitCall(to, code string) (string, error) {
	return redsmsSendWaitCall(to, code)
}
