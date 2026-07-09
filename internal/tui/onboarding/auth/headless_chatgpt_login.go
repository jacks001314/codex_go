package auth

type HeadlessChatGPTLoginState struct {
	RequestID  string
	LoginID    string
	DeviceCode string
	UserCode   string
	URL        string
	Error      string
}

func Pending(requestID string) HeadlessChatGPTLoginState {
	return HeadlessChatGPTLoginState{RequestID: requestID}
}

func Ready(requestID string, loginID string, url string, userCode string) HeadlessChatGPTLoginState {
	return HeadlessChatGPTLoginState{RequestID: requestID, LoginID: loginID, URL: url, UserCode: userCode}
}

func (s HeadlessChatGPTLoginState) IsShowingCopyableAuth() bool {
	return s.URL != "" && s.UserCode != ""
}

func (s HeadlessChatGPTLoginState) Lines() []string {
	if s.IsShowingCopyableAuth() {
		return []string{
			"Finish signing in via your browser",
			"",
			"  1. Open this link in your browser and sign in",
			"",
			"  " + s.URL,
			"",
			"  2. Enter this one-time code after you are signed in (expires in 15 minutes)",
			"",
			"  " + s.UserCode,
			"",
			"  Device codes are a common phishing target. Never share this code.",
			"",
			"  Press Esc to cancel",
		}
	}
	return []string{"Preparing device code login", "", "  Requesting a one-time code...", "", "  Press Esc to cancel"}
}
