package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func getVkCreds(dialCtx func(ctx context.Context, network, address string) (net.Conn, error), link string) (string, string, string, error) {
	doRequest := func(data string, url string) (resp map[string]interface{}, err error) {
		client := &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				DialContext:         dialCtx,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		}
		defer client.CloseIdleConnections()
		req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte(data)))
		if err != nil {
			return nil, err
		}

		req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0")
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		httpResp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer httpResp.Body.Close()

		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(body, &resp)
		if err != nil {
			return nil, err
		}

		return resp, nil
	}

	safeGet := func(m map[string]interface{}, keys ...string) (interface{}, error) {
		var current interface{} = m
		for _, key := range keys {
			mp, ok := current.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("expected map at key %q, got %T", key, current)
			}
			current, ok = mp[key]
			if !ok {
				return nil, fmt.Errorf("key %q not found", key)
			}
		}
		return current, nil
	}

	getString := func(m map[string]interface{}, keys ...string) (string, error) {
		v, err := safeGet(m, keys...)
		if err != nil {
			return "", err
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("expected string, got %T", v)
		}
		return s, nil
	}

	data := "client_secret=QbYic1K3lEV5kTGiqlq2&client_id=6287487&scopes=audio_anonymous%2Cvideo_anonymous%2Cphotos_anonymous%2Cprofile_anonymous&isApiOauthAnonymEnabled=false&version=1&app_id=6287487"
	url := "https://login.vk.ru/?act=get_anonym_token"

	resp, err := doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	token1, err := getString(resp, "data", "access_token")
	if err != nil {
		return "", "", "", fmt.Errorf("get anonym token: %w", err)
	}

	data = fmt.Sprintf("access_token=%s", token1)
	url = "https://api.vk.ru/method/calls.getAnonymousAccessTokenPayload?v=5.264&client_id=6287487"

	resp, err = doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	token2, err := getString(resp, "response", "payload")
	if err != nil {
		return "", "", "", fmt.Errorf("get payload: %w", err)
	}

	data = fmt.Sprintf("client_id=6287487&token_type=messages&payload=%s&client_secret=QbYic1K3lEV5kTGiqlq2&version=1&app_id=6287487", token2)
	url = "https://login.vk.ru/?act=get_anonym_token"

	resp, err = doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	token3, err := getString(resp, "data", "access_token")
	if err != nil {
		return "", "", "", fmt.Errorf("get messages token: %w", err)
	}

	data = fmt.Sprintf("vk_join_link=https://vk.com/call/join/%s&name=123&access_token=%s", link, token3)
	url = "https://api.vk.ru/method/calls.getAnonymousToken?v=5.264"

	resp, err = doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	token4, err := getString(resp, "response", "token")
	if err != nil {
		return "", "", "", fmt.Errorf("get anonymous token: %w", err)
	}

	data = fmt.Sprintf("%s%s%s", "session_data=%7B%22version%22%3A2%2C%22device_id%22%3A%22", uuid.New(), "%22%2C%22client_version%22%3A1.1%2C%22client_type%22%3A%22SDK_JS%22%7D&method=auth.anonymLogin&format=JSON&application_key=CGMMEJLGDIHBABABA")
	url = "https://calls.okcdn.ru/fb.do"

	resp, err = doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	token5, err := getString(resp, "session_key")
	if err != nil {
		return "", "", "", fmt.Errorf("get session_key: %w", err)
	}

	data = fmt.Sprintf("joinLink=%s&isVideo=false&protocolVersion=5&anonymToken=%s&method=vchat.joinConversationByLink&format=JSON&application_key=CGMMEJLGDIHBABABA&session_key=%s", link, token4, token5)
	url = "https://calls.okcdn.ru/fb.do"

	resp, err = doRequest(data, url)
	if err != nil {
		return "", "", "", fmt.Errorf("request error: %w", err)
	}

	user, err := getString(resp, "turn_server", "username")
	if err != nil {
		return "", "", "", fmt.Errorf("get turn username: %w", err)
	}
	pass, err := getString(resp, "turn_server", "credential")
	if err != nil {
		return "", "", "", fmt.Errorf("get turn credential: %w", err)
	}
	turnURLs, err := safeGet(resp, "turn_server", "urls")
	if err != nil {
		return "", "", "", fmt.Errorf("get turn urls: %w", err)
	}
	urlSlice, ok := turnURLs.([]interface{})
	if !ok || len(urlSlice) == 0 {
		return "", "", "", fmt.Errorf("no turn URLs found")
	}
	turnURL, ok := urlSlice[0].(string)
	if !ok {
		return "", "", "", fmt.Errorf("turn URL is not a string")
	}

	clean := strings.Split(turnURL, "?")[0]
	address := strings.TrimPrefix(strings.TrimPrefix(clean, "turn:"), "turns:")

	return user, pass, address, nil
}
