package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

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

type senluopanTokenFields struct {
	AccessToken    string          `json:"access_token"`
	RefreshToken   string          `json:"refresh_token"`
	AccessExpires  json.RawMessage `json:"access_expires"`
	RefreshExpires json.RawMessage `json:"refresh_expires"`
}

type senluopanTokenData struct {
	Token          *senluopanTokenFields `json:"token"`
	AccessToken    string                `json:"access_token"`
	RefreshToken   string                `json:"refresh_token"`
	AccessExpires  json.RawMessage       `json:"access_expires"`
	RefreshExpires json.RawMessage       `json:"refresh_expires"`
}

type senluopanTokenResponse struct {
	Code int                `json:"code"`
	Data senluopanTokenData `json:"data"`
	Msg  string             `json:"msg"`
}

type senluopanUnauthorizedError struct {
	status int
}

func (err senluopanUnauthorizedError) Error() string {
	if err.status == http.StatusForbidden {
		return "森络盘令牌被拒绝"
	}
	return "森络盘令牌已失效"
}

var senluopanAuthMu sync.Mutex

func senluopanProviderConfigured(provider model.StorageProvider) bool {
	if strings.TrimSpace(provider.APIEndpoint) == "" {
		return false
	}
	return strings.TrimSpace(provider.APIAccessToken) != "" ||
		strings.TrimSpace(provider.APIRefreshToken) != "" ||
		(strings.TrimSpace(provider.APIEmail) != "" && strings.TrimSpace(provider.APIPassword) != "")
}

func createSenluopanDirectLink(provider model.StorageProvider, objectKey string) (string, string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	if endpoint == "" {
		return "", "", errors.New("森络盘 API 配置不完整")
	}
	authenticated, err := ensureSenluopanProviderAuth(provider, false)
	if err != nil {
		return "", "", err
	}
	provider = authenticated

	link, linkID, status, err := requestSenluopanDirectLink(provider, objectKey)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return link, linkID, err
	}
	authenticated, authErr := ensureSenluopanProviderAuth(provider, true)
	if authErr != nil {
		return "", "", authErr
	}
	return requestSenluopanDirectLinkResult(authenticated, objectKey)
}

func requestSenluopanDirectLink(provider model.StorageProvider, objectKey string) (string, string, int, error) {
	link, linkID, err := requestSenluopanDirectLinkResult(provider, objectKey)
	if err == nil {
		return link, linkID, 0, nil
	}
	var unauthorized senluopanUnauthorizedError
	if errors.As(err, &unauthorized) {
		return "", "", unauthorizedStatus(err), err
	}
	return "", "", 0, err
}

func requestSenluopanDirectLinkResult(provider model.StorageProvider, objectKey string) (string, string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	token := strings.TrimSpace(provider.APIAccessToken)
	if endpoint == "" || token == "" {
		return "", "", errors.New("森络盘 API 配置不完整")
	}

	// 修复：从 objectKey 中移除 PathPrefix，因为森络盘 WebDAV 的根目录
	// 可能已经指向了 PathPrefix 对应的目录
	pathPrefix := strings.Trim(strings.TrimSpace(provider.PathPrefix), "/")
	relativePath := objectKey
	if pathPrefix != "" && strings.HasPrefix(objectKey, pathPrefix+"/") {
		// 移除 pathPrefix 前缀
		relativePath = strings.TrimPrefix(objectKey, pathPrefix+"/")
	}

	fileURL := url.URL{Scheme: "cloudreve", Host: "my", Path: "/" + strings.TrimLeft(relativePath, "/")}
	body, err := json.Marshal(map[string][]string{"uris": {fileURL.String()}})
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
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return "", "", senluopanUnauthorizedError{status: response.StatusCode}
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
	if endpoint == "" || strings.TrimSpace(directLinkID) == "" {
		return nil
	}
	authenticated, err := ensureSenluopanProviderAuth(provider, false)
	if err != nil {
		return err
	}
	provider = authenticated
	err = requestDeleteSenluopanDirectLink(provider, directLinkID)
	if !isSenluopanUnauthorized(err) {
		return err
	}
	authenticated, err = ensureSenluopanProviderAuth(provider, true)
	if err != nil {
		return err
	}
	return requestDeleteSenluopanDirectLink(authenticated, directLinkID)
}

func requestDeleteSenluopanDirectLink(provider model.StorageProvider, directLinkID string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	token := strings.TrimSpace(provider.APIAccessToken)
	if endpoint == "" || token == "" {
		return errors.New("森络盘 API 配置不完整")
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
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return senluopanUnauthorizedError{status: response.StatusCode}
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

func ensureSenluopanProviderAuth(provider model.StorageProvider, force bool) (model.StorageProvider, error) {
	authenticated, _, err := ensureSenluopanProviderAuthWithPersistence(provider, force)
	return authenticated, err
}

func ensureSenluopanProviderAuthWithPersistence(provider model.StorageProvider, force bool) (model.StorageProvider, bool, error) {
	senluopanAuthMu.Lock()
	defer senluopanAuthMu.Unlock()
	if !force && senluopanAccessTokenValid(provider) {
		return provider, false, nil
	}
	var refreshErr error
	if strings.TrimSpace(provider.APIRefreshToken) != "" && senluopanRefreshTokenValid(provider) {
		fields, err := requestSenluopanToken(provider, "/session/token/refresh", map[string]string{"refresh_token": strings.TrimSpace(provider.APIRefreshToken)})
		if err == nil {
			applySenluopanTokenFields(&provider, fields)
			if strings.TrimSpace(provider.APIAccessToken) != "" {
				persisted, persistErr := persistSenluopanProvider(provider)
				if persistErr != nil {
					return provider, false, persistErr
				}
				return provider, persisted, nil
			}
			refreshErr = errors.New("森络盘刷新响应未返回 Access Token")
		} else {
			refreshErr = err
		}
	}
	if strings.TrimSpace(provider.APIEmail) == "" || strings.TrimSpace(provider.APIPassword) == "" {
		if refreshErr != nil {
			return provider, false, fmt.Errorf("森络盘令牌刷新失败且未配置账号密码: %w", refreshErr)
		}
		return provider, false, errors.New("森络盘 API 未配置账号邮箱和密码")
	}
	fields, err := requestSenluopanToken(provider, "/session/token", map[string]string{"email": strings.TrimSpace(provider.APIEmail), "password": provider.APIPassword})
	if err != nil {
		if refreshErr != nil {
			return provider, false, fmt.Errorf("森络盘令牌刷新和登录均失败: %v; %w", refreshErr, err)
		}
		return provider, false, err
	}
	applySenluopanTokenFields(&provider, fields)
	if strings.TrimSpace(provider.APIAccessToken) == "" {
		return provider, false, errors.New("森络盘认证响应未返回 Access Token")
	}
	persisted, persistErr := persistSenluopanProvider(provider)
	if persistErr != nil {
		return provider, false, persistErr
	}
	return provider, persisted, nil
}

func requestSenluopanToken(provider model.StorageProvider, route string, body map[string]string) (senluopanTokenFields, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	data, err := json.Marshal(body)
	if err != nil {
		return senluopanTokenFields{}, err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint+route, bytes.NewReader(data))
	if err != nil {
		return senluopanTokenFields{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := SafeProxyHTTPClient().Do(request)
	if err != nil {
		return senluopanTokenFields{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return senluopanTokenFields{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return senluopanTokenFields{}, fmt.Errorf("森络盘认证失败: HTTP %d", response.StatusCode)
	}
	var payload senluopanTokenResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return senluopanTokenFields{}, fmt.Errorf("森络盘认证响应无效: %w", err)
	}
	if payload.Code != 0 {
		if strings.TrimSpace(payload.Msg) != "" {
			return senluopanTokenFields{}, fmt.Errorf("森络盘认证失败: %s", strings.TrimSpace(payload.Msg))
		}
		return senluopanTokenFields{}, fmt.Errorf("森络盘认证失败: code=%d", payload.Code)
	}
	if payload.Data.Token != nil && strings.TrimSpace(payload.Data.Token.AccessToken) != "" {
		return *payload.Data.Token, nil
	}
	return senluopanTokenFields{
		AccessToken: payload.Data.AccessToken, RefreshToken: payload.Data.RefreshToken,
		AccessExpires: payload.Data.AccessExpires, RefreshExpires: payload.Data.RefreshExpires,
	}, nil
}

func applySenluopanTokenFields(provider *model.StorageProvider, fields senluopanTokenFields) {
	if strings.TrimSpace(fields.AccessToken) != "" {
		provider.APIAccessToken = strings.TrimSpace(fields.AccessToken)
	}
	if strings.TrimSpace(fields.RefreshToken) != "" {
		provider.APIRefreshToken = strings.TrimSpace(fields.RefreshToken)
	}
	if expires := parseSenluopanExpiry(fields.AccessExpires); expires > 0 {
		provider.APIAccessExpires = expires
	}
	if expires := parseSenluopanExpiry(fields.RefreshExpires); expires > 0 {
		provider.APIRefreshExpires = expires
	}
}

func parseSenluopanExpiry(raw json.RawMessage) int64 {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if value == "" || value == "null" {
		return 0
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		if number > 0 && number < 1_000_000_000 {
			return time.Now().Add(time.Duration(number) * time.Second).Unix()
		}
		return number
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.Unix()
	}
	return 0
}

func senluopanAccessTokenValid(provider model.StorageProvider) bool {
	return strings.TrimSpace(provider.APIAccessToken) != "" && senluopanExpiryValid(provider.APIAccessExpires)
}

func senluopanRefreshTokenValid(provider model.StorageProvider) bool {
	return senluopanExpiryValid(provider.APIRefreshExpires)
}

func senluopanExpiryValid(expires int64) bool {
	return expires <= 0 || time.Unix(expires, 0).After(time.Now().Add(30*time.Second))
}

func unauthorizedStatus(err error) int {
	var unauthorized senluopanUnauthorizedError
	if errors.As(err, &unauthorized) && unauthorized.status > 0 {
		return unauthorized.status
	}
	return http.StatusUnauthorized
}

func isSenluopanUnauthorized(err error) bool {
	var unauthorized senluopanUnauthorizedError
	return errors.As(err, &unauthorized)
}

// SenluopanAuthStatus 森络盘认证状态和容量摘要。
type SenluopanAuthStatus struct {
	Valid          bool   `json:"valid"`
	Renewed        bool   `json:"renewed"`
	Persisted      bool   `json:"persisted"`
	AccessExpires  int64  `json:"accessExpires"`
	RefreshExpires int64  `json:"refreshExpires"`
	TotalBytes     int64  `json:"totalBytes"`
	UsedBytes      int64  `json:"usedBytes"`
	CheckedAt      string `json:"checkedAt"`
}

type senluopanCapacityResponse struct {
	Code int `json:"code"`
	Data struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
	} `json:"data"`
	Msg string `json:"msg"`
}

// CheckSenluopanProviderAuth 检查森络盘 Access Token，并在失效时刷新或重新登录。
func CheckSenluopanProviderAuth(provider model.StorageProvider) (SenluopanAuthStatus, error) {
	if strings.TrimSpace(provider.APIEndpoint) == "" {
		return SenluopanAuthStatus{}, errors.New("森络盘 API 地址不能为空")
	}
	if !senluopanProviderConfigured(provider) {
		return SenluopanAuthStatus{}, errors.New("森络盘 API 配置不完整")
	}
	beforeAccess, beforeRefresh := provider.APIAccessToken, provider.APIRefreshToken
	beforeAccessExpires, beforeRefreshExpires := provider.APIAccessExpires, provider.APIRefreshExpires
	authenticated, persisted, err := ensureSenluopanProviderAuthWithPersistence(provider, false)
	if err != nil {
		return SenluopanAuthStatus{}, err
	}
	total, used, status, err := requestSenluopanCapacity(authenticated)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		var refreshedPersisted bool
		authenticated, refreshedPersisted, err = ensureSenluopanProviderAuthWithPersistence(authenticated, true)
		persisted = persisted || refreshedPersisted
		if err != nil {
			return SenluopanAuthStatus{}, err
		}
		total, used, _, err = requestSenluopanCapacity(authenticated)
	}
	if err != nil {
		return SenluopanAuthStatus{}, err
	}
	return SenluopanAuthStatus{
		Valid:          true,
		Renewed:        beforeAccess != authenticated.APIAccessToken || beforeRefresh != authenticated.APIRefreshToken || beforeAccessExpires != authenticated.APIAccessExpires || beforeRefreshExpires != authenticated.APIRefreshExpires,
		Persisted:      persisted,
		AccessExpires:  authenticated.APIAccessExpires,
		RefreshExpires: authenticated.APIRefreshExpires,
		TotalBytes:     total,
		UsedBytes:      used,
		CheckedAt:      now(),
	}, nil
}

func requestSenluopanCapacity(provider model.StorageProvider) (int64, int64, int, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(provider.APIEndpoint), "/")
	token := strings.TrimSpace(provider.APIAccessToken)
	if endpoint == "" || token == "" {
		return 0, 0, 0, errors.New("森络盘 API 配置不完整")
	}
	request, err := http.NewRequest(http.MethodGet, endpoint+"/user/capacity", nil)
	if err != nil {
		return 0, 0, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := SafeProxyHTTPClient().Do(request)
	if err != nil {
		return 0, 0, 0, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return 0, 0, 0, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return 0, 0, response.StatusCode, senluopanUnauthorizedError{status: response.StatusCode}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, 0, response.StatusCode, fmt.Errorf("森络盘认证检查失败: HTTP %d", response.StatusCode)
	}
	var payload senluopanCapacityResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return 0, 0, response.StatusCode, fmt.Errorf("森络盘容量响应无效: %w", err)
	}
	if payload.Code != 0 {
		if payload.Code == http.StatusUnauthorized || payload.Code == http.StatusForbidden {
			return 0, 0, payload.Code, senluopanUnauthorizedError{status: payload.Code}
		}
		if strings.TrimSpace(payload.Msg) != "" {
			return 0, 0, response.StatusCode, fmt.Errorf("森络盘认证检查失败: %s", strings.TrimSpace(payload.Msg))
		}
		return 0, 0, response.StatusCode, fmt.Errorf("森络盘认证检查失败: code=%d", payload.Code)
	}
	return payload.Data.Total, payload.Data.Used, response.StatusCode, nil
}
