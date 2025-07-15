package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/briandowns/spinner"
)

/*
getFixFromOpenAIWithMeta queries OpenAI's GPT-4 API to suggest a fix for a failed shell command.

Arguments:
- command: The shell command that was attempted.
- errorMsg: The error message received from running the command.
- meta: Optional additional context or notes to help OpenAI provide a better fix.

Returns:
- fix: A shell command string suggested by OpenAI to fix the error.
- explanation: A human-readable explanation of the fix.

This function constructs a prompt including the command, error, OS, and meta note, sends it to OpenAI,
and expects a strict JSON response with 'fix' and 'explanation'.
*/
func getFixFromOpenAIWithMeta(command, errorMsg, meta string) (string, string) {
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " 🧠 Thinking for a fix..."
	s.Start()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", "Missing OPENAI_API_KEY"
	}
	userOs := runtime.GOOS
	userContent := fmt.Sprintf(`I ran this command: %s\nIt failed with this error: %s\ni am on this OS : %s`, command, errorMsg, userOs)
	if meta != "" {
		userContent += fmt.Sprintf("\nNote: %s", meta)
	}
	userContent += `\nSuggest a command to fix this and explain why. In response only returnReturn JSON like below, no extra text aprt from below json object, not even a single letter strictly\n{"fix": "...", "explanation": "..."}`
	payload := map[string]interface{}{
		"model": "gpt-4",
		"messages": []map[string]string{
			{"role": "system", "content": "You're an expert terminal assistant."},
			{"role": "user", "content": userContent},
		},
	}
	data, _ := json.Marshal(payload)
	curlCmd := exec.Command("curl", "-s", "https://api.openai.com/v1/chat/completions",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+apiKey,
		"-d", string(data),
	)
	output, err := curlCmd.Output()
	if err != nil {
		return "", "Failed to fetch fix from OpenAI"
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", "Failed to parse OpenAI response"
	}
	content := result.Choices[0].Message.Content
	var fixResp struct {
		Fix         string `json:"fix"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(content), &fixResp); err != nil {
		return "", content
	}
	s.Stop()
	return fixResp.Fix, fixResp.Explanation
}
