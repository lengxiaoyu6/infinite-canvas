package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/repository"
)

type UserConfigPayload struct {
	ModelConfig      json.RawMessage       `json:"modelConfig,omitempty"`
	StorageProvider  *UserStorageProviders `json:"storageProvider,omitempty"`
	ImageHistory     json.RawMessage       `json:"imageHistory,omitempty"`
	AssetData        json.RawMessage       `json:"assetData,omitempty"`
	SyncCapabilities map[string]bool       `json:"syncCapabilities,omitempty"`
}

type StorageObjectProviderInput struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Endpoint        string `json:"endpoint"`
	APIEndpoint       string `json:"apiEndpoint"`
	APIAccessToken    string `json:"apiAccessToken"`
	APIRefreshToken   string `json:"apiRefreshToken"`
	APIEmail          string `json:"apiEmail"`
	APIPassword       string `json:"apiPassword"`
	APIAccessExpires  int64  `json:"apiAccessExpires"`
	APIRefreshExpires int64  `json:"apiRefreshExpires"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	PublicBaseURL   string `json:"publicBaseUrl"`
	PathPrefix      string `json:"pathPrefix"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type UserStorageProviders struct {
	S3     *StorageObjectProviderInput `json:"s3,omitempty"`
	WebDAV *StorageObjectProviderInput `json:"webdav,omitempty"`
}

type userModelConfigInput struct {
	Model           string                       `json:"model"`
	ImageModel      string                       `json:"imageModel"`
	VideoModel      string                       `json:"videoModel"`
	TextModel       string                       `json:"textModel"`
	AudioModel      string                       `json:"audioModel"`
	ActiveChannelID string                       `json:"activeChannelId"`
	ImageChannelID  string                       `json:"imageChannelId"`
	VideoChannelID  string                       `json:"videoChannelId"`
	TextChannelID   string                       `json:"textChannelId"`
	AudioChannelID  string                       `json:"audioChannelId"`
	LocalChannels   []userLocalModelChannelInput `json:"localChannels"`
}

type userLocalModelChannelInput struct {
	ID              string   `json:"id"`
	SystemChannelID string   `json:"systemChannelId"`
	Protocol        string   `json:"protocol"`
	Name            string   `json:"name"`
	BaseURL         string   `json:"baseUrl"`
	APIKey          string   `json:"apiKey"`
	Models          []string `json:"models"`
}

func SelectUserLocalModelChannelForModel(userID string, modelName string, channelID string) (model.ModelChannel, error) {
	userID = strings.TrimSpace(userID)
	modelName = strings.TrimSpace(modelName)
	channelID = strings.TrimSpace(channelID)
	if userID == "" {
		return model.ModelChannel{}, errors.New("请先登录")
	}
	if modelName == "" {
		return model.ModelChannel{}, errors.New("缺少模型名称")
	}
	if channelID == "" {
		return model.ModelChannel{}, errors.New("缺少模型渠道")
	}
	config, ok, err := repository.GetUserConfig(userID)
	if err != nil {
		return model.ModelChannel{}, err
	}
	if !ok || strings.TrimSpace(config.ModelConfig) == "" {
		return model.ModelChannel{}, errors.New("个人密钥渠道不存在")
	}
	var modelConfig userModelConfigInput
	if err := json.Unmarshal([]byte(config.ModelConfig), &modelConfig); err != nil {
		return model.ModelChannel{}, err
	}
	settings, err := repository.GetSettings()
	if err != nil {
		return model.ModelChannel{}, err
	}
	settings = normalizeSettings(settings)
	if settings.Public.ModelChannel.AllowCustomChannel == nil || !*settings.Public.ModelChannel.AllowCustomChannel {
		return model.ModelChannel{}, errors.New("个人密钥渠道未开放")
	}
	availableModels := settings.Public.ModelChannel.AvailableModels
	if len(availableModels) == 0 {
		availableModels = enabledChannelModels(settings.Private.Channels)
	}
	if len(availableModels) > 0 && !modelListHasModel(availableModels, modelName) {
		return model.ModelChannel{}, errors.New("个人密钥渠道未开放该模型")
	}
	for _, channel := range modelConfig.LocalChannels {
		localID := strings.TrimSpace(channel.ID)
		systemChannelID := strings.TrimSpace(channel.SystemChannelID)
		if localID != channelID && systemChannelID != channelID {
			continue
		}
		baseURL := strings.TrimSpace(channel.BaseURL)
		protocol := strings.ToLower(strings.TrimSpace(channel.Protocol))
		if protocol == "" {
			protocol = "openai"
		}
		apiKey := strings.TrimSpace(channel.APIKey)
		models := userLocalChannelModels(channel.Models)

		// Legacy personal channels are identified by systemChannelId.
		if systemChannelID != "" {
			var systemChannel model.ModelChannel
			for _, item := range settings.Private.Channels {
				if strings.TrimSpace(item.ID) == systemChannelID {
					systemChannel = item
					break
				}
			}
			if systemChannel.ID == "" || !systemChannel.Enabled || strings.TrimSpace(systemChannel.BaseURL) == "" {
				return model.ModelChannel{}, errors.New("个人密钥渠道不可用")
			}
			if !modelListHasModel(systemChannel.Models, modelName) {
				return model.ModelChannel{}, errors.New("个人密钥渠道不支持该模型")
			}
			apiKey = strings.TrimSpace(channel.APIKey)
			if apiKey == "" {
				return model.ModelChannel{}, errors.New("个人密钥渠道未填写 API Key")
			}
			if len(models) > 0 && !userLocalChannelHasModel(models, modelName) {
				return model.ModelChannel{}, errors.New("个人密钥渠道不支持该模型")
			}
			systemChannel.APIKey = apiKey
			return systemChannel, nil
		}
		if baseURL == "" || apiKey == "" {
			return model.ModelChannel{}, errors.New("本地渠道配置不完整")
		}
		if len(models) > 0 && !userLocalChannelHasModel(models, modelName) {
			return model.ModelChannel{}, errors.New("本地渠道不支持该模型")
		}
		return model.ModelChannel{
			ID:       channelID,
			Protocol: protocol,
			Name:     firstVideoTaskValue(strings.TrimSpace(channel.Name), "本地直连"),
			BaseURL:  baseURL,
			APIKey:   apiKey,
			Models:   models,
			Weight:   1,
			Timeout:  600,
			Enabled:  true,
		}, nil
	}
	return model.ModelChannel{}, errors.New("个人密钥渠道不存在")
}

func userLocalChannelModels(models []string) []string {
	result := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, item := range models {
		modelName := strings.TrimSpace(item)
		if modelName == "" || seen[modelName] {
			continue
		}
		result = append(result, modelName)
		seen[modelName] = true
	}
	return result
}

func modelListHasModel(models []string, modelName string) bool {
	for _, item := range models {
		if strings.EqualFold(strings.TrimSpace(item), modelName) {
			return true
		}
	}
	return false
}

func CurrentUserConfig(ctx context.Context) (UserConfigPayload, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return UserConfigPayload{}, errors.New("请先登录")
	}
	config, ok, err := repository.GetUserConfig(user.ID)
	if err != nil {
		return UserConfigPayload{}, err
	}
	result := UserConfigPayload{
		SyncCapabilities: map[string]bool{
			"userData":  true,
			"workflows": true,
			"assets":    true,
		},
	}
	if !ok {
		return result, nil
	}
	if strings.TrimSpace(config.ModelConfig) != "" {
		result.ModelConfig = json.RawMessage(config.ModelConfig)
	}
	if strings.TrimSpace(config.StorageProvider) != "" {
		providers := readUserStorageProviders(config.StorageProvider)
		var syncFlags struct {
			SyncStorageConfig       bool `json:"syncStorageConfig"`
			SyncWebDAVStorageConfig bool `json:"syncWebDAVStorageConfig"`
		}
		_ = json.Unmarshal(result.ModelConfig, &syncFlags)
		if !syncFlags.SyncStorageConfig {
			providers.S3 = nil
		}
		if !syncFlags.SyncWebDAVStorageConfig {
			providers.WebDAV = nil
		}
		if providers.S3 != nil || providers.WebDAV != nil {
			result.StorageProvider = &providers
		}
	}
	if strings.TrimSpace(config.ImageHistory) != "" {
		result.ImageHistory = json.RawMessage(config.ImageHistory)
	}
	if strings.TrimSpace(config.AssetData) != "" {
		result.AssetData = json.RawMessage(config.AssetData)
	}
	return result, nil
}

func readUserStorageProviders(raw string) UserStorageProviders {
	var providers UserStorageProviders
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &providers)
	}
	return providers
}

func SaveCurrentUserModelConfig(ctx context.Context, raw json.RawMessage) (UserConfigPayload, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return UserConfigPayload{}, errors.New("请先登录")
	}
	config, _, err := repository.GetUserConfig(user.ID)
	if err != nil {
		return UserConfigPayload{}, err
	}
	current := now()
	if config.UserID == "" {
		config.UserID = user.ID
		config.CreatedAt = current
	}
	cleaned, err := sanitizeUserModelConfig(raw)
	if err != nil {
		return UserConfigPayload{}, err
	}
	config.ModelConfig = string(cleaned)
	config.UpdatedAt = current
	if _, err := repository.SaveUserConfig(config); err != nil {
		return UserConfigPayload{}, err
	}
	return CurrentUserConfig(ctx)
}

func sanitizeUserModelConfig(raw json.RawMessage) (json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var input userModelConfigInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	settings, err := repository.GetSettings()
	if err != nil {
		return nil, err
	}
	settings = normalizeSettings(settings)
	allowPersonalChannel := settings.Public.ModelChannel.AllowCustomChannel != nil && *settings.Public.ModelChannel.AllowCustomChannel
	availableModels := settings.Public.ModelChannel.AvailableModels
	if len(availableModels) == 0 {
		availableModels = enabledChannelModels(settings.Private.Channels)
	}
	availableModelSet := make(map[string]bool, len(availableModels))
	for _, name := range availableModels {
		if name = strings.TrimSpace(name); name != "" {
			availableModelSet[strings.ToLower(name)] = true
		}
	}
	channelByID := make(map[string]model.ModelChannel, len(settings.Private.Channels))
	for _, channel := range settings.Private.Channels {
		if !channel.Enabled || strings.TrimSpace(channel.BaseURL) == "" {
			continue
		}
		channel.Models = filterEnabledModels(channel.Models, availableModels)
		if len(channel.Models) > 0 {
			channelByID[strings.TrimSpace(channel.ID)] = channel
		}
	}

	localChannels := make([]userLocalModelChannelInput, 0, len(input.LocalChannels))
	selectableChannels := make(map[string]model.ModelChannel, len(channelByID)+len(input.LocalChannels))
	for id, channel := range channelByID {
		selectableChannels[id] = channel
	}
	seen := map[string]bool{}
	for _, item := range input.LocalChannels {
		id := strings.TrimSpace(item.ID)
		systemID := strings.TrimSpace(item.SystemChannelID)
		models := userLocalChannelModels(item.Models)
		if systemID != "" {
			channel, ok := channelByID[systemID]
			if !allowPersonalChannel || !ok || seen[systemID] {
				continue
			}
			localChannels = append(localChannels, userLocalModelChannelInput{
				ID:              systemID,
				SystemChannelID: systemID,
				APIKey:          strings.TrimSpace(item.APIKey),
				Models:          userLocalChannelModels(channel.Models),
			})
			seen[systemID] = true
			selectableChannels[systemID] = channel
			continue
		}
		if id == "" || !allowPersonalChannel || seen[id] || strings.TrimSpace(item.BaseURL) == "" || strings.TrimSpace(item.APIKey) == "" {
			continue
		}
		hadModels := len(models) > 0
		if hadModels && len(availableModelSet) > 0 {
			filtered := make([]string, 0, len(models))
			for _, name := range models {
				if availableModelSet[strings.ToLower(name)] {
					filtered = append(filtered, name)
				}
			}
			models = filtered
			if len(models) == 0 {
				continue
			}
		}
		protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
		if protocol == "" {
			protocol = "openai"
		}
		local := userLocalModelChannelInput{
			ID:       id,
			Protocol: protocol,
			Name:     strings.TrimSpace(item.Name),
			BaseURL:  strings.TrimSpace(item.BaseURL),
			APIKey:   strings.TrimSpace(item.APIKey),
			Models:   models,
		}
		localChannels = append(localChannels, local)
		seen[id] = true
		selectableChannels[id] = model.ModelChannel{ID: id, Protocol: protocol, Name: local.Name, BaseURL: local.BaseURL, APIKey: local.APIKey, Models: models, Enabled: true}
	}
	encodedChannels, err := json.Marshal(localChannels)
	if err != nil {
		return nil, err
	}
	payload["localChannels"] = encodedChannels
	sanitizeUserModelSelection(payload, "model", "activeChannelId", input.Model, input.ActiveChannelID, selectableChannels, availableModelSet)
	sanitizeUserModelSelection(payload, "imageModel", "imageChannelId", input.ImageModel, input.ImageChannelID, selectableChannels, availableModelSet)
	sanitizeUserModelSelection(payload, "videoModel", "videoChannelId", input.VideoModel, input.VideoChannelID, selectableChannels, availableModelSet)
	sanitizeUserModelSelection(payload, "textModel", "textChannelId", input.TextModel, input.TextChannelID, selectableChannels, availableModelSet)
	sanitizeUserModelSelection(payload, "audioModel", "audioChannelId", input.AudioModel, input.AudioChannelID, selectableChannels, availableModelSet)
	delete(payload, "baseUrl")
	delete(payload, "apiKey")
	delete(payload, "publicChannels")
	delete(payload, "models")
	delete(payload, "imageModels")
	delete(payload, "videoModels")
	delete(payload, "textModels")
	delete(payload, "audioModels")
	return json.Marshal(payload)
}

func sanitizeUserModelSelection(payload map[string]json.RawMessage, modelKey string, channelKey string, modelName string, channelID string, channels map[string]model.ModelChannel, availableModels map[string]bool) {
	modelName = strings.TrimSpace(modelName)
	channelID = strings.TrimSpace(channelID)
	if modelName == "" || (len(availableModels) > 0 && !availableModels[strings.ToLower(modelName)]) {
		delete(payload, modelKey)
		delete(payload, channelKey)
		return
	}
	payload[modelKey], _ = json.Marshal(modelName)
	channel, ok := channels[channelID]
	if !ok || (len(channel.Models) > 0 && !modelListHasModel(channel.Models, modelName)) {
		delete(payload, channelKey)
		return
	}
	payload[channelKey], _ = json.Marshal(channelID)
}

func CurrentUserImageHistory(ctx context.Context) (json.RawMessage, error) {
	config, err := currentUserConfig(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.ImageHistory) == "" {
		return json.RawMessage(`{"logs":[],"categories":[]}`), nil
	}
	return json.RawMessage(config.ImageHistory), nil
}

func SaveCurrentUserImageHistory(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	config, err := saveCurrentUserConfigField(ctx, func(config *model.UserConfig) {
		config.ImageHistory = string(raw)
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(config.ImageHistory), nil
}

func CurrentUserAssetData(ctx context.Context) (json.RawMessage, error) {
	config, err := currentUserConfig(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AssetData) == "" {
		return json.RawMessage(`{"assets":[]}`), nil
	}
	return json.RawMessage(config.AssetData), nil
}

func SaveCurrentUserAssetData(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	config, err := saveCurrentUserConfigField(ctx, func(config *model.UserConfig) {
		config.AssetData = string(raw)
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(config.AssetData), nil
}

func currentUserConfig(ctx context.Context) (model.UserConfig, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return model.UserConfig{}, errors.New("请先登录")
	}
	config, _, err := repository.GetUserConfig(user.ID)
	if err != nil {
		return model.UserConfig{}, err
	}
	if config.UserID == "" {
		config.UserID = user.ID
	}
	return config, nil
}

func saveCurrentUserConfigField(ctx context.Context, patch func(config *model.UserConfig)) (model.UserConfig, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return model.UserConfig{}, errors.New("请先登录")
	}
	config, _, err := repository.GetUserConfig(user.ID)
	if err != nil {
		return model.UserConfig{}, err
	}
	current := now()
	if config.UserID == "" {
		config.UserID = user.ID
		config.CreatedAt = current
	}
	patch(&config)
	config.UpdatedAt = current
	return repository.SaveUserConfig(config)
}
