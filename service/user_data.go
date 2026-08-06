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
	if !modelListHasModel(availableModels, modelName) {
		return model.ModelChannel{}, errors.New("个人密钥渠道未开放该模型")
	}
	for _, channel := range modelConfig.LocalChannels {
		systemChannelID := strings.TrimSpace(channel.SystemChannelID)
		if systemChannelID == "" {
			systemChannelID = strings.TrimSpace(channel.ID)
		}
		if systemChannelID != channelID {
			continue
		}
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
		apiKey := strings.TrimSpace(channel.APIKey)
		if apiKey == "" {
			return model.ModelChannel{}, errors.New("个人密钥渠道未填写 API Key")
		}
		systemChannel.APIKey = apiKey
		return systemChannel, nil
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
	channels := settings.Private.Channels
	allowPersonalChannel := settings.Public.ModelChannel.AllowCustomChannel != nil && *settings.Public.ModelChannel.AllowCustomChannel
	availableModels := settings.Public.ModelChannel.AvailableModels
	if len(availableModels) == 0 {
		availableModels = enabledChannelModels(channels)
	}
	channelByID := make(map[string]model.ModelChannel, len(channels))
	for _, channel := range channels {
		if channel.Enabled && strings.TrimSpace(channel.BaseURL) != "" {
			channel.Models = filterEnabledModels(channel.Models, availableModels)
			if len(channel.Models) > 0 {
				channelByID[channel.ID] = channel
			}
		}
	}
	localChannels := make([]userLocalModelChannelInput, 0, len(input.LocalChannels))
	seen := map[string]bool{}
	for _, item := range input.LocalChannels {
		systemChannelID := strings.TrimSpace(item.SystemChannelID)
		if systemChannelID == "" {
			systemChannelID = strings.TrimSpace(item.ID)
		}
		channel, ok := channelByID[systemChannelID]
		if !allowPersonalChannel || !ok || seen[systemChannelID] {
			continue
		}
		localChannels = append(localChannels, userLocalModelChannelInput{
			ID:              systemChannelID,
			SystemChannelID: systemChannelID,
			APIKey:          strings.TrimSpace(item.APIKey),
			Models:          userLocalChannelModels(channel.Models),
		})
		seen[systemChannelID] = true
	}
	encodedChannels, err := json.Marshal(localChannels)
	if err != nil {
		return nil, err
	}
	payload["localChannels"] = encodedChannels
	sanitizeUserModelSelection(payload, "model", "activeChannelId", input.Model, input.ActiveChannelID, channelByID)
	sanitizeUserModelSelection(payload, "imageModel", "imageChannelId", input.ImageModel, input.ImageChannelID, channelByID)
	sanitizeUserModelSelection(payload, "videoModel", "videoChannelId", input.VideoModel, input.VideoChannelID, channelByID)
	sanitizeUserModelSelection(payload, "textModel", "textChannelId", input.TextModel, input.TextChannelID, channelByID)
	sanitizeUserModelSelection(payload, "audioModel", "audioChannelId", input.AudioModel, input.AudioChannelID, channelByID)
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

func sanitizeUserModelSelection(payload map[string]json.RawMessage, modelKey string, channelKey string, modelName string, channelID string, channels map[string]model.ModelChannel) {
	modelName = strings.TrimSpace(modelName)
	channelID = strings.TrimSpace(channelID)
	modelAvailable := false
	for _, channel := range channels {
		if modelListHasModel(channel.Models, modelName) {
			modelAvailable = true
			break
		}
	}
	if !modelAvailable {
		delete(payload, modelKey)
		delete(payload, channelKey)
		return
	}
	payload[modelKey], _ = json.Marshal(modelName)
	channel, ok := channels[channelID]
	if !ok || !modelListHasModel(channel.Models, modelName) {
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
