package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/service"
)

func StartVideoTaskPoller() {
	service.StartVideoTaskPoller(pollVideoTaskFromUpstream)
}

func UserVideoTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	tasks, err := service.ListUserVideoTasks(user.ID, "video-workbench", 100)
	if err != nil {
		log.Printf("list video tasks failed: user=%s err=%v", user.ID, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, tasks)
}

func DeleteUserVideoTask(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		Fail(w, "视频任务不存在")
		return
	}
	if err := service.DeleteUserVideoTask(user.ID, id); err != nil {
		log.Printf("delete video task failed: user=%s id=%s err=%v", user.ID, id, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, map[string]any{"deleted": true})
}

func proxyAIVideoTaskRequest(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	body, contentType, modelName, err := readAIRequest(r)
	if err != nil {
		log.Printf("AI video request read failed: %v", err)
		Fail(w, "AI 接口请求失败")
		return
	}
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	channel, userChannelID, err := selectAIRequestChannel(user, modelName, r.Header.Get("X-Model-Channel-ID"), r.Header.Get(userModelChannelHeader))
	if err != nil {
		log.Printf("AI video select channel failed: model=%s err=%v", modelName, err)
		failAIChannelSelect(w, err, "AI 接口请求失败")
		return
	}
	credits := 0
	if userChannelID == "" {
		credits, err = service.ModelCost(modelName)
		if err != nil {
			log.Printf("AI video read model cost failed: model=%s err=%v", modelName, err)
			Fail(w, "AI 接口请求失败")
			return
		}
		credits *= readAIRequestCount(body, contentType)
	}
	upstreamPath := resolveAIProxyPath(channel, modelName, "/videos")
	body, contentType, err = normalizeVideoCreateBody(body, contentType, modelName, channel, upstreamPath)
	if err != nil {
		log.Printf("AI video normalize request failed: model=%s err=%v", modelName, err)
		Fail(w, firstNonEmpty(err.Error(), "AI 接口请求失败"))
		return
	}
	request, err := http.NewRequest(http.MethodPost, service.BuildModelChannelURL(channel, upstreamPath), bytes.NewReader(body))
	if err != nil {
		log.Printf("AI video build request failed: url=%s err=%v", service.BuildModelChannelURL(channel, upstreamPath), err)
		Fail(w, "AI 接口请求失败")
		return
	}
	service.SetModelChannelAuthHeader(request, channel)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	logContext := aiLogContext{
		StartedAt:       startedAt,
		Endpoint:        "/videos",
		Method:          http.MethodPost,
		Model:           modelName,
		Channel:         channel,
		UserID:          user.ID,
		UserDisplayName: firstNonEmpty(user.DisplayName, user.Username),
		Credits:         credits,
		RequestBody:     summarizeAIRequest(body, contentType),
	}
	if credits > 0 {
		if err := service.ConsumeUserCredits(user.ID, modelName, credits, upstreamPath); err != nil {
			FailError(w, err)
			return
		}
	}
	payload, status, err := doAIRequest(request, channel)
	if err != nil {
		if credits > 0 {
			refundVideoCredits(user.ID, modelName, credits, upstreamPath)
		}
		saveAIProxyLog(logContext, 0, "", err.Error())
		Fail(w, "AI 接口请求失败")
		return
	}
	if status >= http.StatusBadRequest {
		message := readUpstreamAIErrorMessage(payload, status)
		if credits > 0 {
			refundVideoCredits(user.ID, modelName, credits, upstreamPath)
		}
		saveAIProxyLog(logContext, status, string(payload), strings.TrimSpace(string(payload)))
		Fail(w, message)
		return
	}
	transformed := transformVideoCreatePayload(payload, request, channel, modelName)
	if message := readVideoCreateErrorMessage(payload, transformed, channel, modelName); message != "" {
		if credits > 0 {
			refundVideoCredits(user.ID, modelName, credits, upstreamPath)
		}
		saveAIProxyLog(logContext, status, string(payload), message)
		Fail(w, message)
		return
	}
	parsed := parseVideoTaskPayload(transformed, modelName)
	if parsed.UpstreamTaskID == "" && parsed.UpstreamVideoID == "" {
		if credits > 0 {
			refundVideoCredits(user.ID, modelName, credits, upstreamPath)
		}
		saveAIProxyLog(logContext, status, string(transformed), "视频接口没有返回任务 ID")
		Fail(w, "视频接口没有返回任务 ID")
		return
	}
	task, err := service.CreateVideoTask(service.VideoTaskCreateInput{
		UserID:          user.ID,
		UserDisplayName: firstNonEmpty(user.DisplayName, user.Username),
		Model:           modelName,
		ChannelID:       channel.ID,
		UserChannelID:   userChannelID,
		ChannelName:     channel.Name,
		Source:          readVideoTaskSource(r),
		SourceID:        readVideoTaskSourceID(r),
		ClientTaskID:    readClientVideoTaskID(r),
		UpstreamTaskID:  parsed.UpstreamTaskID,
		UpstreamVideoID: parsed.UpstreamVideoID,
		Status:          parsed.Status,
		Progress:        parsed.Progress,
		Seconds:         parsed.Seconds,
		Size:            parsed.Size,
		VideoURL:        parsed.VideoURL,
		Error:           parsed.Error,
		ErrorDetail:     parsed.ErrorDetail,
		RequestBody:     logContext.RequestBody,
		ResponseBody:    string(transformed),
		Credits:         credits,
	})
	if err != nil {
		log.Printf("save video task failed: model=%s err=%v", modelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	saveAIProxyLog(logContext, status, string(transformed), "")
	OK(w, service.VideoTaskResponse(task))
}

func readClientVideoTaskID(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-Client-Video-Task-ID"))
	if isClientVideoTaskID(id) {
		return id
	}
	return ""
}

func readVideoTaskSource(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Video-Task-Source"))
}

func readVideoTaskSourceID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Video-Task-Source-ID"))
}

func isClientVideoTaskID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), "client_video_task_")
}

func serveAIVideoTask(w http.ResponseWriter, r *http.Request, id string) bool {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		return false
	}
	task, found, err := service.GetUserVideoTask(user.ID, id)
	if err != nil {
		log.Printf("read video task failed: id=%s user=%s err=%v", id, user.ID, err)
		Fail(w, "AI 接口请求失败")
		return true
	}
	if !found {
		return false
	}
	OK(w, service.VideoTaskResponse(task))
	return true
}

func serveGeminiVideoTaskContent(w http.ResponseWriter, r *http.Request, id string) bool {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		return false
	}
	task, found, err := service.GetUserVideoTask(user.ID, strings.TrimSpace(id))
	if err != nil || !found {
		return false
	}
	var channel model.ModelChannel
	if strings.TrimSpace(task.UserChannelID) != "" {
		channel, err = service.SelectUserLocalModelChannelForModel(task.UserID, task.Model, task.UserChannelID)
	} else {
		channel, err = service.SelectModelChannelForModel(task.Model, task.ChannelID)
	}
	if err != nil || !service.IsGeminiChannel(channel) {
		return false
	}
	if strings.TrimSpace(task.VideoURL) == "" {
		Fail(w, "Gemini Veo 任务完成但没有返回视频地址")
		return true
	}
	request, err := http.NewRequest(http.MethodGet, task.VideoURL, nil)
	if err != nil {
		Fail(w, "视频内容下载失败")
		return true
	}
	service.SetModelChannelAuthHeader(request, channel)
	response, err := service.HTTPClientForChannel(channel).Do(request)
	if err != nil {
		Fail(w, "视频内容下载失败")
		return true
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		Fail(w, readUpstreamAIErrorMessage(nil, response.StatusCode))
		return true
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
	return true
}

func pollVideoTaskFromUpstream(task model.VideoTask) (service.VideoTaskPollUpdate, error) {
	var channel model.ModelChannel
	var err error
	if strings.TrimSpace(task.UserChannelID) != "" {
		channel, err = service.SelectUserLocalModelChannelForModel(task.UserID, task.Model, task.UserChannelID)
	} else {
		channel, err = service.SelectModelChannelForModel(task.Model, task.ChannelID)
	}
	if err != nil {
		return service.VideoTaskPollUpdate{}, err
	}
	pollID := firstNonEmpty(task.UpstreamTaskID, task.ID)
	if isAIProtocolVideoID(task.Model, task.UpstreamVideoID) {
		pollID = task.UpstreamVideoID
	}
	if strings.TrimSpace(pollID) == "" {
		return service.VideoTaskPollUpdate{}, errors.New("视频任务缺少上游任务 ID")
	}
	endpoint := "/videos/" + pollID
	upstreamPath := resolveAIProxyPath(channel, task.Model, endpoint)
	request, err := http.NewRequest(http.MethodGet, resolveAIProxyURL(channel, task.Model, upstreamPath), nil)
	if err != nil {
		return service.VideoTaskPollUpdate{}, err
	}
	service.SetModelChannelAuthHeader(request, channel)
	startedAt := time.Now()
	logContext := aiLogContext{
		StartedAt:       startedAt,
		Endpoint:        endpoint,
		Method:          http.MethodGet,
		Model:           task.Model,
		Channel:         channel,
		UserID:          task.UserID,
		UserDisplayName: task.UserDisplayName,
		RequestBody:     fmt.Sprintf(`{"taskId":%q}`, pollID),
	}
	payload, status, err := doAIRequest(request, channel)
	if err != nil {
		saveAIProxyLog(logContext, 0, "", err.Error())
		return service.VideoTaskPollUpdate{}, err
	}
	if status >= http.StatusBadRequest {
		message := readUpstreamAIErrorMessage(payload, status)
		saveAIProxyLog(logContext, status, string(payload), strings.TrimSpace(string(payload)))
		if status == http.StatusTooManyRequests {
			return service.VideoTaskPollUpdate{Status: task.Status, ErrorDetail: message, ResponseBody: string(payload)}, nil
		}
		return service.VideoTaskPollUpdate{Status: "failed", Error: message, ErrorDetail: message, ResponseBody: string(payload)}, nil
	}
	transformed := transformVideoStatusPayload(payload, request, channel, task.Model)
	parsed := parseVideoTaskPayload(transformed, task.Model)
	if parsed.Status == "failed" && parsed.Error == "" {
		parsed.Error = firstNonEmpty(parsed.ErrorDetail, "视频任务生成失败")
	}
	if errMessage := readVideoStatusErrorMessage(payload, transformed, channel, task.Model); errMessage != "" {
		if parsed.Error == "" {
			parsed.Error = errMessage
		}
		parsed.Status = "failed"
	}
	if parsed.ErrorDetail == "" && len(payload) > 0 && parsed.Error != "" {
		parsed.ErrorDetail = string(payload)
	}
	saveAIProxyLog(logContext, status, string(transformed), firstNonEmpty(parsed.Error, ""))
	return service.VideoTaskPollUpdate{
		Status:       parsed.Status,
		Progress:     parsed.Progress,
		Seconds:      parsed.Seconds,
		Size:         parsed.Size,
		VideoURL:     parsed.VideoURL,
		Error:        parsed.Error,
		ErrorDetail:  parsed.ErrorDetail,
		ResponseBody: string(transformed),
	}, nil
}

func normalizeVideoCreateBody(body []byte, contentType string, modelName string, channel model.ModelChannel, upstreamPath string) ([]byte, string, error) {
	prepared, protocolID, err := prepareAIProtocolRequest(aiProtocolRequest{
		mode: aiProtocolVideoRequest, body: body, contentType: contentType, modelName: modelName,
		channel: channel, endpoint: "/videos", path: upstreamPath,
	})
	if err != nil || protocolID != "" {
		return prepared.body, prepared.contentType, err
	}
	if (upstreamPath == "/videos" || upstreamPath == "/videos/generations") && isXAICompatibleVideoModel(modelName) {
		return normalizeXAICompatibleVideoBody(body, contentType, modelName)
	}
	return body, contentType, nil
}

func isXAICompatibleVideoModel(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "grok-imagine-video")
}

// 前端 grok2api 直连已按 xAI 原生格式组装请求体（含时长、内联好的 data URI 参考图），
// 这类请求体无需转换，直接透传；仅当仍带 http(s) 参考图需要服务端内联时才重写。
func isPreparedXAICompatibleVideoBody(payload xaiCompatibleVideoPayload, body []byte) bool {
	if len(payload.Values["model"]) == 0 || firstXAICompatibleVideoValue(payload, "prompt") == "" {
		return false
	}
	return !bytes.Contains(body, []byte("http://")) && !bytes.Contains(body, []byte("https://"))
}

type xaiCompatibleVideoPayload struct {
	Values map[string][]string
	Files  map[string][]xaiCompatibleVideoFile
}

type xaiCompatibleVideoFile struct {
	ContentType string
	Data        []byte
}

func normalizeXAICompatibleVideoBody(body []byte, contentType string, modelName string) ([]byte, string, error) {
	payload, err := readXAICompatibleVideoPayload(body, contentType)
	if err != nil {
		return body, contentType, err
	}
	if errMessage := readXAICompatibleVideoUnsupportedReferenceError(payload); errMessage != "" {
		return body, contentType, errors.New(errMessage)
	}
	if isPreparedXAICompatibleVideoBody(payload, body) {
		return body, contentType, nil
	}

	prompt := strings.TrimSpace(firstXAICompatibleVideoValue(payload, "prompt"))
	if prompt == "" {
		return body, contentType, errors.New("xAI 视频模型缺少提示词")
	}
	durationText := firstXAICompatibleVideoValue(payload, "duration", "seconds")
	duration, err := strconv.Atoi(strings.TrimSpace(durationText))
	if err != nil || duration < 1 {
		return body, contentType, errors.New("xAI 视频模型时长参数无效")
	}

	primaryImages, referenceImages, err := collectXAICompatibleVideoImageReferences(payload)
	if err != nil {
		return body, contentType, err
	}
	result := map[string]any{
		"model":        strings.TrimSpace(modelName),
		"prompt":       prompt,
		"duration":     duration,
		"aspect_ratio": normalizeXAICompatibleVideoAspectRatio(firstXAICompatibleVideoValue(payload, "aspect_ratio"), firstXAICompatibleVideoValue(payload, "size")),
		"resolution":   normalizeXAICompatibleVideoResolution(firstXAICompatibleVideoValue(payload, "resolution"), firstXAICompatibleVideoValue(payload, "resolution_name")),
	}
	if len(primaryImages) > 0 {
		result["image"] = primaryImages[0]
		referenceImages = append(primaryImages[1:], referenceImages...)
	}
	if len(referenceImages) > 0 {
		result["reference_images"] = referenceImages
	}
	if referenceAudios := xaiCompatibleVideoVoiceReferences(payload); len(referenceAudios) > 0 {
		result["reference_audios"] = referenceAudios
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return body, contentType, err
	}
	return encoded, "application/json", nil
}

func readXAICompatibleVideoPayload(body []byte, contentType string) (xaiCompatibleVideoPayload, error) {
	payload := xaiCompatibleVideoPayload{Values: map[string][]string{}, Files: map[string][]xaiCompatibleVideoFile{}}
	if !strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			return payload, err
		}
		for key, item := range value {
			addXAICompatibleVideoJSONValue(&payload, key, item)
		}
		return payload, nil
	}

	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return payload, err
	}
	form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(32 << 20)
	if err != nil {
		return payload, err
	}
	defer form.RemoveAll()
	for key, values := range form.Value {
		for _, value := range values {
			addXAICompatibleVideoValue(&payload, key, value)
		}
	}
	for key, files := range form.File {
		for _, item := range files {
			if isXAICompatibleVideoUnsupportedFileField(key) {
				payload.Files[key] = append(payload.Files[key], xaiCompatibleVideoFile{ContentType: item.Header.Get("Content-Type")})
				continue
			}
			file, err := item.Open()
			if err != nil {
				return payload, err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, 32<<20+1))
			_ = file.Close()
			if readErr != nil {
				return payload, readErr
			}
			if len(data) > 32<<20 {
				return payload, errors.New("xAI 视频参考图片最大支持 32MB")
			}
			payload.Files[key] = append(payload.Files[key], xaiCompatibleVideoFile{ContentType: item.Header.Get("Content-Type"), Data: data})
		}
	}
	return payload, nil
}

func isXAICompatibleVideoUnsupportedFileField(key string) bool {
	switch strings.TrimSpace(key) {
	case "video_reference[]", "video_reference", "video_url", "video_urls", "video", "videos", "reference_video", "reference_videos", "reference_video_url", "reference_video_urls", "input_video_url", "input_video_urls":
		return true
	case "audio_reference[]", "audio_reference", "audio_url", "audio_urls", "audio", "audios", "reference_audio", "reference_audios", "reference_audio_url", "reference_audio_urls", "reference_voice", "input_audio_url", "input_audio_urls":
		return true
	default:
		return false
	}
}

func addXAICompatibleVideoJSONValue(payload *xaiCompatibleVideoPayload, key string, value any) {
	switch typed := value.(type) {
	case nil:
		return
	case []any:
		for _, item := range typed {
			addXAICompatibleVideoJSONValue(payload, key, item)
		}
	case map[string]any:
		if text := strings.TrimSpace(firstNonEmpty(readXAICompatibleVideoJSONMapString(typed, "url"), readXAICompatibleVideoJSONMapString(typed, "image_url"))); text != "" {
			addXAICompatibleVideoValue(payload, key+".url", text)
		}
		if text := strings.TrimSpace(readXAICompatibleVideoJSONMapString(typed, "file_id")); text != "" {
			addXAICompatibleVideoValue(payload, key+".file_id", text)
		}
		if text := strings.TrimSpace(readXAICompatibleVideoJSONMapString(typed, "voice_id")); text != "" {
			addXAICompatibleVideoValue(payload, key+".voice_id", text)
		}
	default:
		addXAICompatibleVideoValue(payload, key, toStringSafe(value))
	}
}

func readXAICompatibleVideoJSONMapString(value map[string]any, key string) string {
	if item, ok := value[key]; ok {
		return strings.TrimSpace(toStringSafe(item))
	}
	return ""
}

func addXAICompatibleVideoValue(payload *xaiCompatibleVideoPayload, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	payload.Values[key] = append(payload.Values[key], value)
}

func readXAICompatibleVideoUnsupportedReferenceError(payload xaiCompatibleVideoPayload) string {
	if hasXAICompatibleVideoInput(payload, []string{"video_reference[]", "video_reference", "video_url", "video_urls", "video", "videos", "reference_video", "reference_videos", "reference_video_url", "reference_video_urls", "input_video_url", "input_video_urls"}) {
		return "xAI 视频编辑需要使用 /videos/edits，当前视频生成入口暂不支持参考视频"
	}
	if hasXAICompatibleVideoInput(payload, []string{"audio_reference[]", "audio_reference", "audio_url", "audio_urls", "audio", "audios", "reference_audio_url", "reference_audio_urls", "input_audio_url", "input_audio_urls"}) || hasXAICompatibleVideoNonVoiceAudioReference(payload) {
		return "xAI 视频模型的参考音频仅支持 voice_id 预设音色，当前上传音频文件或音频 URL 无法转换"
	}
	return ""
}

func hasXAICompatibleVideoInput(payload xaiCompatibleVideoPayload, keys []string) bool {
	for _, key := range keys {
		if len(payload.Values[key]) > 0 || len(payload.Values[key+".url"]) > 0 || len(payload.Values[key+".file_id"]) > 0 || len(payload.Files[key]) > 0 {
			return true
		}
	}
	return false
}

func hasXAICompatibleVideoNonVoiceAudioReference(payload xaiCompatibleVideoPayload) bool {
	for _, key := range []string{"reference_audio", "reference_audios", "reference_voice"} {
		if len(payload.Values[key+".url"]) > 0 || len(payload.Values[key+".file_id"]) > 0 || len(payload.Files[key]) > 0 {
			return true
		}
		for _, value := range payload.Values[key] {
			if isXAICompatibleVideoURLValue(value) {
				return true
			}
		}
	}
	return false
}

func isXAICompatibleVideoURLValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "blob:")
}

func firstXAICompatibleVideoValue(payload xaiCompatibleVideoPayload, keys ...string) string {
	for _, key := range keys {
		for _, value := range payload.Values[key] {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func collectXAICompatibleVideoImageReferences(payload xaiCompatibleVideoPayload) ([]map[string]string, []map[string]string, error) {
	primaryKeys := []string{"image", "image_url", "input_url", "first_frame_url", "first_frame_image"}
	referenceKeys := []string{"images", "image_urls", "input_urls", "input_reference", "input_reference[]", "reference_image", "reference_images", "reference_image_url", "reference_image_urls", "last_frame_url", "last_frame_image"}
	primary, err := collectXAICompatibleVideoReferencesForKeys(payload, primaryKeys)
	if err != nil {
		return nil, nil, err
	}
	references, err := collectXAICompatibleVideoReferencesForKeys(payload, referenceKeys)
	if err != nil {
		return nil, nil, err
	}
	return primary, references, nil
}

func collectXAICompatibleVideoReferencesForKeys(payload xaiCompatibleVideoPayload, keys []string) ([]map[string]string, error) {
	result := []map[string]string{}
	for _, key := range keys {
		for _, value := range payload.Values[key] {
			uri, err := xaiCompatibleVideoReferenceURLToDataURI(value)
			if err != nil {
				return nil, err
			}
			appendXAICompatibleVideoReference(&result, "url", uri)
		}
		for _, value := range payload.Values[key+".url"] {
			uri, err := xaiCompatibleVideoReferenceURLToDataURI(value)
			if err != nil {
				return nil, err
			}
			appendXAICompatibleVideoReference(&result, "url", uri)
		}
		for _, value := range payload.Values[key+".file_id"] {
			appendXAICompatibleVideoReference(&result, "file_id", value)
		}
		for _, file := range payload.Files[key] {
			uri, err := xaiCompatibleVideoFileDataURI(file)
			if err != nil {
				return nil, err
			}
			appendXAICompatibleVideoReference(&result, "url", uri)
		}
	}
	return result, nil
}

// xaiCompatibleVideoReferenceURLToDataURI 把 http(s) 参考图链接在服务端下载并内联成
// base64 data URI。部分对象存储直链（如森络盘）会 302 跳转到国内移动云主机，海外的 xAI
// 上游解析该域名 DNS 超时而无法下载，改为服务端下载后内联可绕开上游的网络访问。
// data URI、file_id 等非 http(s) 值原样返回。
func xaiCompatibleVideoReferenceURLToDataURI(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return trimmed, nil
	}
	request, err := http.NewRequest(http.MethodGet, trimmed, nil)
	if err != nil {
		return "", err
	}
	response, err := service.SafeProxyHTTPClient().Do(request)
	if err != nil {
		return "", fmt.Errorf("下载 xAI 视频参考图片失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("下载 xAI 视频参考图片失败: %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, referenceImageMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("下载 xAI 视频参考图片失败: %w", err)
	}
	if int64(len(data)) > referenceImageMaxBytes {
		return "", errors.New("xAI 视频参考图片超过大小限制")
	}
	return xaiCompatibleVideoFileDataURI(xaiCompatibleVideoFile{
		ContentType: response.Header.Get("Content-Type"),
		Data:        data,
	})
}

func appendXAICompatibleVideoReference(result *[]map[string]string, field string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*result = append(*result, map[string]string{field: value})
}

func xaiCompatibleVideoFileDataURI(file xaiCompatibleVideoFile) (string, error) {
	contentType := normalizeXAICompatibleVideoContentType(file.ContentType, file.Data)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(file.Data), nil
	default:
		return "", errors.New("xAI 视频参考图片仅支持 JPEG、PNG 或 WebP")
	}
}

func normalizeXAICompatibleVideoContentType(contentType string, data []byte) string {
	value := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if value == "" || value == "application/octet-stream" {
		value = strings.ToLower(http.DetectContentType(data))
	}
	if value == "image/jpg" {
		return "image/jpeg"
	}
	return value
}

func xaiCompatibleVideoVoiceReferences(payload xaiCompatibleVideoPayload) []map[string]string {
	result := []map[string]string{}
	for _, key := range []string{"voice_id", "reference_voice", "reference_audio", "reference_audios"} {
		for _, value := range payload.Values[key] {
			appendXAICompatibleVideoReference(&result, "voice_id", value)
		}
		for _, value := range payload.Values[key+".voice_id"] {
			appendXAICompatibleVideoReference(&result, "voice_id", value)
		}
	}
	return result
}

func normalizeXAICompatibleVideoAspectRatio(aspectRatio string, size string) string {
	value := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(firstNonEmpty(aspectRatio, size))), " ", "")
	switch value {
	case "9:16", "720x1280", "1080x1920":
		return "9:16"
	case "1:1", "720x720", "1024x1024", "1080x1080":
		return "1:1"
	default:
		return "16:9"
	}
}

func normalizeXAICompatibleVideoResolution(values ...string) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(values...)))
	switch value {
	case "480p", "720p", "1080p":
		return value
	case "low":
		return "480p"
	default:
		return "720p"
	}
}

func doAIRequest(request *http.Request, channel model.ModelChannel) ([]byte, int, error) {
	response, err := service.HTTPClientForChannel(channel).Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	return payload, response.StatusCode, nil
}

func transformVideoCreatePayload(payload []byte, request *http.Request, channel model.ModelChannel, modelName string) []byte {
	return transformAIProtocolVideoPayload(payload, request, channel, modelName, false)
}

func transformVideoStatusPayload(payload []byte, request *http.Request, channel model.ModelChannel, modelName string) []byte {
	return transformAIProtocolVideoPayload(payload, request, channel, modelName, true)
}

func transformGeminiVideoTaskResponse(payload []byte) ([]byte, bool) {
	var root map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &root) != nil {
		return nil, false
	}
	name := readStringPath(root, "name")
	done, _ := root["done"].(bool)
	videoURL := findFirstHTTPURL(root)
	errorMessage := firstNonEmpty(readStringPath(root, "error.message"))
	status := "processing"
	progress := 0
	if errorMessage != "" {
		status = "failed"
	} else if done && videoURL != "" {
		status = "completed"
		progress = 100
	} else if done {
		status = "failed"
		errorMessage = "Gemini Veo 任务完成但没有返回视频地址"
	}
	transformed, err := json.Marshal(map[string]any{
		"id":        name,
		"task_id":   name,
		"status":    status,
		"progress":  progress,
		"video_url": videoURL,
		"error":     map[string]any{"message": errorMessage},
	})
	return transformed, err == nil
}

func readVideoCreateErrorMessage(raw []byte, transformed []byte, channel model.ModelChannel, modelName string) string {
	return firstNonEmpty(readAIProtocolVideoError(raw, channel, modelName, false), readProviderPayloadError(raw), readNormalizedVideoError(transformed))
}

func readVideoStatusErrorMessage(raw []byte, transformed []byte, channel model.ModelChannel, modelName string) string {
	return firstNonEmpty(readAIProtocolVideoError(raw, channel, modelName, true), readProviderPayloadError(raw), readNormalizedVideoError(transformed))
}

type parsedVideoTaskPayload struct {
	UpstreamTaskID  string
	UpstreamVideoID string
	Status          string
	Progress        int
	Seconds         string
	Size            string
	VideoURL        string
	Error           string
	ErrorDetail     string
}

func parseVideoTaskPayload(payload []byte, modelName string) parsedVideoTaskPayload {
	var root any
	if len(payload) == 0 || json.Unmarshal(payload, &root) != nil {
		return parsedVideoTaskPayload{Status: "processing"}
	}
	data := normalizeVideoPayloadMap(root)
	result := parsedVideoTaskPayload{
		UpstreamTaskID:  firstNonEmpty(readStringPath(data, "task_id"), readStringPath(data, "taskId"), readStringPath(data, "id"), readStringPath(data, "request_id")),
		UpstreamVideoID: firstNonEmpty(readStringPath(data, "video_id"), readStringPath(data, "videoId")),
		Status:          service.NormalizeVideoTaskStatus(firstNonEmpty(readStringPath(data, "status"), readStringPath(data, "state"), readStringPath(data, "task_status"))),
		Progress:        readIntPath(data, "progress"),
		Seconds:         firstNonEmpty(readStringPath(data, "seconds"), readStringPath(data, "duration")),
		Size:            firstNonEmpty(readStringPath(data, "size"), readSizeFromDimensions(data)),
		VideoURL:        firstNonEmpty(readStringPath(data, "video.url"), readStringPath(data, "video_url"), readStringPath(data, "url"), readStringPath(data, "remixed_from_video_id"), readStringPath(data, "output_url"), readStringPath(data, "download_url"), findFirstHTTPURL(data)),
		Error:           firstNonEmpty(readStringPath(data, "error.message"), readStringPath(data, "error")),
		ErrorDetail:     "",
	}
	if result.UpstreamTaskID == result.UpstreamVideoID && strings.HasPrefix(result.UpstreamVideoID, "video_") {
		result.UpstreamTaskID = ""
	}
	if result.Status == "" {
		result.Status = "processing"
	}
	if result.VideoURL != "" {
		result.Status = "completed"
		result.Progress = 100
	}
	if result.Status == "failed" && result.Error == "" {
		result.Error = firstNonEmpty(readStringPath(data, "message"), readStringPath(data, "msg"), "视频任务生成失败")
	}
	if result.UpstreamVideoID == "" && isAIProtocolVideoID(modelName, result.VideoURL) {
		result.UpstreamVideoID = result.VideoURL
	}
	if result.Error != "" {
		result.ErrorDetail = string(payload)
	}
	return result
}

func normalizeVideoPayloadMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if data, ok := typed["data"].(map[string]any); ok {
			for key, item := range typed {
				if _, exists := data[key]; !exists {
					data[key] = item
				}
			}
			return data
		}
		if data, ok := typed["data"].([]any); ok && len(data) > 0 {
			if item, ok := data[0].(map[string]any); ok {
				for key, value := range typed {
					if _, exists := item[key]; !exists {
						item[key] = value
					}
				}
				return item
			}
		}
		return typed
	default:
		return map[string]any{}
	}
}

func readNormalizedVideoError(payload []byte) string {
	parsed := parseVideoTaskPayload(payload, "")
	if parsed.Status == "failed" || parsed.Error != "" {
		return firstNonEmpty(parsed.Error, "视频任务生成失败")
	}
	return ""
}

func readProviderPayloadError(payload []byte) string {
	var value map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &value) != nil {
		return ""
	}
	code, hasCode := value["code"]
	if !hasCode {
		return ""
	}
	successCode := false
	switch typed := code.(type) {
	case float64:
		successCode = typed == 0 || typed == 200
	case string:
		text := strings.TrimSpace(strings.ToLower(typed))
		successCode = text == "" || text == "0" || text == "200" || text == "success" || text == "ok"
	default:
		successCode = false
	}
	if successCode {
		return ""
	}
	return firstNonEmpty(readStringPath(value, "error.message"), readStringPath(value, "error"), readStringPath(value, "message"), readStringPath(value, "msg"), fmt.Sprint(code))
}

func readStringPath(data map[string]any, path string) string {
	var current any = data
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[part]
	}
	return strings.TrimSpace(toStringSafe(current))
}

func readIntPath(data map[string]any, key string) int {
	value := data[key]
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	case string:
		var number int
		_, _ = fmt.Sscanf(strings.TrimSpace(typed), "%d", &number)
		return number
	default:
		return 0
	}
}

func readSizeFromDimensions(data map[string]any) string {
	width := readIntPath(data, "width")
	height := readIntPath(data, "height")
	if width > 0 && height > 0 {
		return fmt.Sprintf("%dx%d", width, height)
	}
	return ""
}

func findFirstHTTPURL(value any) string {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
			return text
		}
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			return findFirstHTTPURL(parsed)
		}
	case []any:
		for _, item := range typed {
			if url := findFirstHTTPURL(item); url != "" {
				return url
			}
		}
	case map[string]any:
		for _, key := range []string{"uri", "url", "video_url", "videoUrl", "download_url", "downloadUrl", "output_url", "outputUrl", "resultUrls", "result_urls", "videoUrls", "video_urls", "urls", "videos", "video_result", "video", "generatedSamples", "generateVideoResponse", "response", "data", "result", "metadata"} {
			if url := findFirstHTTPURL(typed[key]); url != "" {
				return url
			}
		}
	}
	return ""
}

func refundVideoCredits(userID string, modelName string, credits int, endpoint string) {
	if err := service.RefundUserCredits(userID, modelName, credits, endpoint); err != nil {
		log.Printf("AI video refund credits failed: user=%s model=%s credits=%d err=%v", userID, modelName, credits, err)
	}
}
