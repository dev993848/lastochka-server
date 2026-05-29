package tel

import "strings"

func IsRedsmsConfigured() bool {
	return redsmsClient.client != nil && strings.TrimSpace(redsmsClient.login) != "" && strings.TrimSpace(redsmsClient.apiKey) != ""
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
