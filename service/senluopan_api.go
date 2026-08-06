package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
)

type senluopanDirectLinkResponse struct {
	Code int `json:"code"`
	Data []struct {
		Link    string `json:"link"`
		FileURL string `json:"file_url"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type senluopanResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func createSenluopanDirectLink(provider model.StorageProvider, objectKey string) (string, string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	token := strings.TrimSpace(provider.APIAccessToken)
	if endpoint == "" || token == "" {
		return "", "", errors.New("森络盘 API 配置不完整")
	}
	fileURI := url.URL{Scheme: "cloudreve", Host: "my", Path: "/" + strings.TrimLeft(objectKey, "/")}.String()
	body, err := json.Marshal(map[string][]string{"uris": {fileURI}})
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequest(http.MethodPut, endpoint+"/file/source", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := SafeProxyHTTPClient().Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("森络盘创建直链失败: HTTP %d", response.StatusCode)
	}
	var payload senluopanDirectLinkResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", "", fmt.Errorf("森络盘创建直链响应无效: %w", err)
	}
	if payload.Code != 0 {
		if strings.TrimSpace(payload.Msg) != "" {
			return "", "", fmt.Errorf("森络盘创建直链失败: %s", strings.TrimSpace(payload.Msg))
		}
		return "", "", fmt.Errorf("森络盘创建直链失败: code=%d", payload.Code)
	}
	if len(payload.Data) == 0 || strings.TrimSpace(payload.Data[0].Link) == "" {
		return "", "", errors.New("森络盘创建直链未返回地址")
	}
	link := strings.TrimSpace(payload.Data[0].Link)
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", errors.New("森络盘创建直链地址无效")
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || segments[0] != "f" || segments[1] == "" {
		return "", "", errors.New("森络盘创建直链未返回直链 ID")
	}
	return link, segments[1], nil
}

func deleteSenluopanDirectLink(provider model.StorageProvider, directLinkID string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	token := strings.TrimSpace(provider.APIAccessToken)
	if endpoint == "" || token == "" || strings.TrimSpace(directLinkID) == "" {
		return nil
	}
	request, err := http.NewRequest(http.MethodDelete, endpoint+"/file/source/"+url.PathEscape(strings.TrimSpace(directLinkID)), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := SafeProxyHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("森络盘删除直链失败: HTTP %d", response.StatusCode)
	}
	var payload senluopanResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return fmt.Errorf("森络盘删除直链响应无效: %w", err)
	}
	if payload.Code != 0 {
		if strings.TrimSpace(payload.Msg) != "" {
			return fmt.Errorf("森络盘删除直链失败: %s", strings.TrimSpace(payload.Msg))
		}
		return fmt.Errorf("森络盘删除直链失败: code=%d", payload.Code)
	}
	return nil
}
