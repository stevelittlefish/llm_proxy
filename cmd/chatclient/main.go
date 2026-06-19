package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type serverConfig struct {
	Host string
	Port int
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string         `json:"model"`
	Messages []message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]int `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Message message `json:"message"`
	Done    bool    `json:"done"`
	Error   string  `json:"error,omitempty"`
}

type openAIChatRequest struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Delta *message `json:"delta,omitempty"`
	} `json:"choices"`
}

func main() {
	configPath := flag.String("config", "config.toml", "path to server config file")
	model := flag.String("model", "", "model name to use")
	baseURL := flag.String("url", "", "proxy base URL override")
	openAI := flag.Bool("openai", false, "use OpenAI-compatible API instead of Ollama API")
	maxTokens := flag.Int("max-tokens", 0, "maximum response tokens to request; omitted when 0")
	flag.Parse()

	if *maxTokens < 0 {
		exitf("max-tokens must be 0 or greater")
	}

	if *baseURL == "" {
		cfg, err := loadServerConfig(*configPath)
		if err != nil {
			exitf("failed to load config: %v", err)
		}
		*baseURL = cfg.baseURL()
	}
	*baseURL = strings.TrimRight(*baseURL, "/")

	client := &http.Client{}
	if *model == "" {
		discovered, err := discoverModel(client, *baseURL, *openAI)
		if err == nil {
			*model = discovered
		}
	}
	if *model == "" {
		fmt.Print("Model: ")
		line, err := readLine(bufio.NewReader(os.Stdin))
		if err != nil {
			exitf("failed to read model: %v", err)
		}
		*model = strings.TrimSpace(line)
	}
	if *model == "" {
		exitf("model is required")
	}

	apiName := "Ollama"
	if *openAI {
		apiName = "OpenAI"
	}
	fmt.Printf("llm_proxy chat client (%s API)\n", apiName)
	fmt.Printf("Connected to %s, model %s\n", *baseURL, *model)
	if *maxTokens > 0 {
		fmt.Printf("Maximum response tokens: %d\n", *maxTokens)
	}
	fmt.Println("Commands: /quit, /clear, /model NAME")

	var history []message
	input := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\nYou> ")
		line, err := readLine(input)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println()
				return
			}
			exitf("failed to read input: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case line == "/quit" || line == "/exit":
			return
		case line == "/clear":
			history = nil
			fmt.Println("History cleared.")
			continue
		case strings.HasPrefix(line, "/model "):
			*model = strings.TrimSpace(strings.TrimPrefix(line, "/model "))
			history = nil
			fmt.Printf("Model set to %s. History cleared.\n", *model)
			continue
		case strings.HasPrefix(line, "/"):
			fmt.Println("Unknown command.")
			continue
		}

		history = append(history, message{Role: "user", Content: line})
		fmt.Print("Assistant> ")

		var reply string
		if *openAI {
			reply, err = sendOpenAIChat(client, *baseURL, *model, history, *maxTokens)
		} else {
			reply, err = sendOllamaChat(client, *baseURL, *model, history, *maxTokens)
		}
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			history = history[:len(history)-1]
			continue
		}
		fmt.Println()
		history = append(history, message{Role: "assistant", Content: reply})
	}
}

func sendOllamaChat(client *http.Client, baseURL string, model string, history []message, maxTokens int) (string, error) {
	reqBody := ollamaChatRequest{
		Model:    model,
		Messages: history,
		Stream:   true,
	}
	if maxTokens > 0 {
		reqBody.Options = map[string]int{"num_predict": maxTokens}
	}
	resp, err := postJSON(client, baseURL+"/api/chat", reqBody)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	decoder := json.NewDecoder(resp.Body)
	var reply strings.Builder
	for {
		var chunk ollamaChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return reply.String(), err
		}
		if chunk.Error != "" {
			return reply.String(), errors.New(chunk.Error)
		}
		if chunk.Message.Content != "" {
			fmt.Print(chunk.Message.Content)
			reply.WriteString(chunk.Message.Content)
		}
		if chunk.Done {
			break
		}
	}
	return reply.String(), nil
}

func sendOpenAIChat(client *http.Client, baseURL string, model string, history []message, maxTokens int) (string, error) {
	reqBody := openAIChatRequest{
		Model:    model,
		Messages: history,
		Stream:   true,
	}
	if maxTokens > 0 {
		reqBody.MaxTokens = maxTokens
	}
	resp, err := postJSON(client, baseURL+"/v1/chat/completions", reqBody)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var reply strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "[DONE]" {
			break
		}

		var chunk openAIChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta == nil || choice.Delta.Content == "" {
				continue
			}
			fmt.Print(choice.Delta.Content)
			reply.WriteString(choice.Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return reply.String(), err
	}
	return reply.String(), nil
}

func postJSON(client *http.Client, url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

func discoverModel(client *http.Client, baseURL string, openAI bool) (string, error) {
	if openAI {
		resp, err := client.Get(baseURL + "/v1/models")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var modelsResp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
			return "", err
		}
		if len(modelsResp.Data) == 0 {
			return "", errors.New("no models returned")
		}
		return modelsResp.Data[0].ID, nil
	}

	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var modelsResp struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return "", err
	}
	if len(modelsResp.Models) == 0 {
		return "", errors.New("no models returned")
	}
	if modelsResp.Models[0].Name != "" {
		return modelsResp.Models[0].Name, nil
	}
	return modelsResp.Models[0].Model, nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

func loadServerConfig(path string) (serverConfig, error) {
	cfg := serverConfig{
		Host: "0.0.0.0",
		Port: 11434,
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if section != "server" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "host":
			cfg.Host = strings.Trim(value, `"`)
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				return cfg, fmt.Errorf("invalid server.port: %w", err)
			}
			cfg.Port = port
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func stripComment(line string) string {
	inQuote := false
	for i, r := range line {
		switch r {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

func (c serverConfig) baseURL() string {
	host := c.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	if strings.Contains(host, ":") && net.ParseIP(host) != nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, c.Port)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
